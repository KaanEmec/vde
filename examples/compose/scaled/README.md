# vde scaled: one API, two workers, PostgreSQL and S3

A development setup of [horizontal scaling](../../../docs/operator/operations.md#process-roles):
`vde serve --role api` takes the requests, two `vde serve --role worker` processes run the
jobs, and they share the queue through PostgreSQL and the documents through an S3-compatible
bucket (RustFS). Same provider, model and `dev-app` application as the
[single-process example](../README.md).

| Service | Image | Purpose |
| --- | --- | --- |
| `postgres` | `postgres:17-alpine` | The job store: applications, tokens, jobs (the queue), quotas, shared rate counters, audit. |
| `rustfs` | `rustfs/rustfs:1.0.1` | The blob store (documents and page images). MinIO's image is no longer pullable from Docker Hub; RustFS speaks the same S3 API. |
| `bucket` | `amazon/aws-cli:2.27.0` | Creates the `vde-documents` bucket, then exits. |
| `migrate` | vde | `vde migrate` once before the replicas start, then exits. |
| `api` | vde | `/v1`, the admin API, `/healthz` with `queue_age_seconds`, `/metrics`. Port 8090 on the host's loopback. Runs no job. |
| `worker` (×2) | vde | The worker pool and the sweeper (one sweeps at a time); `/healthz` and `/metrics` only, not published. |

## Start

```
cd examples/compose/scaled
cp .env.example .env        # set ANTHROPIC_API_KEY
docker compose up -d        # or --build to build this checkout
docker compose ps           # api, worker-1 and worker-2 become healthy
curl -sS http://127.0.0.1:8090/healthz     # {"status":"ok","queue_age_seconds":0}
```

`queue_age_seconds` is how long the oldest queued job has waited. It grows when no worker
leases: alert on it (it never makes the API unhealthy, so the orchestrator does not restart
the API for a worker problem).

## Submit and watch the workers

```
export VDE_TOKEN=$(docker compose exec -T api vde token create \
  --app dev-app --scope jobs --expires 1d --label scaled)
cd ../../..
curl -sS http://127.0.0.1:8090/v1/jobs -H "Authorization: Bearer $VDE_TOKEN" \
  -F "document=@examples/samples/booking.txt;type=text/plain; charset=utf-8" \
  -F "extractor_instructions=<examples/samples/extractor.md;type=text/markdown" \
  -F "judge_instructions=<examples/samples/judge.md;type=text/markdown" \
  -F "retention=none"
cd examples/compose/scaled
docker compose logs -f worker | grep "job finished"   # worker_id names the process that ran it
```

A job waits in the `jobs` table until a worker's next poll (`worker.poll_interval`, 500 ms):
the API cannot wake another process's workers.

## Scaling and failure

- More workers: `docker compose up -d --scale worker=4`. Each runs `worker.size` (4) jobs at
  once; size `storage.max_open_conns` and PostgreSQL's `max_connections` for all of them.
- Kill a worker mid-job: `docker kill vde-scaled-worker-1`. Its job keeps its lease until the lease
  expires (`worker.lease`, 2 minutes), then another worker leases it and resumes after the
  last persisted attempt. A stopped worker (`docker compose stop`) drains for up to 30 s first.
- More API replicas need a load balancer in front; with `rate.shared: true` (set here)
  `requests_per_minute` and `submits_per_minute` hold across them.

## Operator commands

Any vde container reads the same database:

```
docker compose exec api vde app list
docker compose exec api vde audit list --since 1h --kind job.finished   # input_tokens, output_tokens
```

## Cleanup

`docker compose down -v` deletes the database and the bucket's volume.

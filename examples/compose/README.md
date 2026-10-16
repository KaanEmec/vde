# vde with docker compose

A development setup: the `vde` service with one provider (Anthropic), one model for both
roles and one application, `dev-app`. About ten minutes from clone to a first job.

| File | Purpose |
| --- | --- |
| [`docker-compose.yaml`](docker-compose.yaml) | The `vde` service, a named data volume, port 8090 on loopback; a commented Caddy service; an optional `prometheus` profile. |
| [`vde.yaml`](vde.yaml) | The configuration: [`../vde.dev.yaml`](../vde.dev.yaml) plus `server` and the `dev-app` application. |
| [`.env.example`](.env.example) | The variables compose reads: the provider key, the image tag. |
| [`Caddyfile`](Caddyfile) | TLS termination for the commented `caddy` service. |
| [`prometheus.yml`](prometheus.yml) | Scrape configuration for the `prometheus` profile. |
| [`scaled/`](scaled/README.md) | A separate example: one `--role api` and two `--role worker` processes on PostgreSQL and RustFS (S3). |

> **Status.** v0.2 is implemented; this example has not yet been run against a real
> provider (see the [changelog](../../CHANGELOG.md)). `docker compose config` validates the
> compose file without an API key if you set a dummy `ANTHROPIC_API_KEY`.

## 1. Start

```
cd examples/compose
cp .env.example .env        # set ANTHROPIC_API_KEY in it; .env is ignored by git
docker compose up -d
docker compose ps           # vde should become healthy
```

This pulls `ghcr.io/kaanemec/vde:latest`. To run the code in this checkout instead:
`docker compose up -d --build`. To pin a release, set `VDE_IMAGE_TAG=0.2.0` in `.env`.

Check the configuration without starting anything and without a network call:

```
docker compose run --rm --no-deps vde config check --config /etc/vde/vde.yaml
```

The service answers on `http://127.0.0.1:8090` only; the port is not published on other
interfaces.

```
curl -sS http://127.0.0.1:8090/healthz     # {"status": "ok"}
```

## 2. Create a token

Tokens are issued by the operator on the command line, against the same database the
service uses (the `vde-data` volume). The token is printed once and only its hash is stored:

```
export VDE_TOKEN=$(docker compose exec -T vde vde token create \
  --app dev-app --scope jobs --expires 2027-12-31 --label "compose dev")
```

`--expires` takes a date or a duration and is required. Rotate by creating a second token
before revoking the first (`vde token list --app dev-app`, `vde token revoke <token id>`).
The token goes to stdout alone; the confirmation goes to stderr.

## 3. Submit a document

The samples in [`../samples/`](../samples/) are synthetic. From the repository root:

```
cd ../..
curl -sS http://127.0.0.1:8090/v1/jobs \
  -H "Authorization: Bearer $VDE_TOKEN" \
  -F "document=@examples/samples/booking.txt;type=text/plain; charset=utf-8" \
  -F "extractor_instructions=<examples/samples/extractor.md;type=text/markdown" \
  -F "judge_instructions=<examples/samples/judge.md;type=text/markdown" \
  -F "schema=<examples/samples/schema.json;type=application/schema+json" \
  -F "retention=none"
```

The answer is `202` with a job in status `queued` and an `id`.

## 4. Poll the job

```
curl -sS http://127.0.0.1:8090/v1/jobs/<id> -H "Authorization: Bearer $VDE_TOKEN"
```

Repeat until `status` is `accepted`, `rejected` or `failed` (usually seconds). `result` holds
the extraction, `feedback` the judge's objections when it was rejected, `attempts` how many
rounds it took. With `retention: none` the job disappears once you have read its terminal
state.

## 5. Read the quota

```
curl -si http://127.0.0.1:8090/v1/quota -H "Authorization: Bearer $VDE_TOKEN"
```

The body and the `X-Quota-Remaining` and `X-Quota-Reset` headers show what is left of
`dev-app`'s 1000 submissions per day (see `quota` in [`vde.yaml`](vde.yaml)).

## Reverse proxy with TLS (optional)

For a production-like run, uncomment the `caddy` service and the `caddy-*` volumes in
[`docker-compose.yaml`](docker-compose.yaml), set `server.trusted_proxy: true` in
[`vde.yaml`](vde.yaml) (requests must then carry `X-Forwarded-Proto: https`, which Caddy
sets), set `VDE_PUBLIC_HOST` in `.env`, and remove the `127.0.0.1:8090:8090` mapping of `vde`
so that only Caddy is reachable. With `VDE_PUBLIC_HOST=localhost` Caddy uses its own local
certificate authority, which is enough to try it (`curl -k https://localhost/healthz`).

## Operator commands

The operator commands read the database in the `vde-data` volume directly; they work while
the service runs and need no `--config` inside the container:

```
docker compose exec vde vde app list
docker compose exec vde vde token list --app dev-app
docker compose exec vde vde audit list --since 24h --app dev-app   # also: --kind, --limit
```

## Admin API

`vde serve` also runs the admin API (`/admin/v1`, admin tokens only) on `admin.listen`, which
defaults to `127.0.0.1:8091` inside the container. That address is not reachable through a
published port, and nothing is published for it by default. Two ways to use it:

- Create admin tokens in the container. They exist only in the database and are created on
  the command line: `docker compose exec -T vde vde token create --admin --expires 30d
  --label ops` (list them with `vde token list --admin`). The image has no HTTP client, so
  calling the API needs the second option.
- Publish it on the host's loopback: set `admin: {listen: ":8091"}` in [`vde.yaml`](vde.yaml)
  (allowed because the file sets `insecure_http`; with TLS use `server.tls`), uncomment the
  `127.0.0.1:8091:8091` port in [`docker-compose.yaml`](docker-compose.yaml), run
  `docker compose up -d`, and use `curl -H "Authorization: Bearer $ADMIN_TOKEN"
  http://127.0.0.1:8091/admin/v1/applications`. Never publish it beyond loopback without TLS.

Reference: [admin API](../../docs/api/admin.md).

## Prometheus and `/metrics` (optional)

`/metrics` answers only loopback peers by default, so it is not reachable through the published
port or from another container. To scrape it, protect it with a bearer token:

1. Uncomment `metrics.token_env: VDE_METRICS_TOKEN` in [`vde.yaml`](vde.yaml) and the
   `VDE_METRICS_TOKEN` line of the `vde` service in [`docker-compose.yaml`](docker-compose.yaml).
2. Set `VDE_METRICS_TOKEN` in `.env` (the example value is a placeholder; use
   `openssl rand -hex 24`). `vde` refuses to start if the named variable is empty.
3. Recreate the service: `docker compose up -d`.

Scrape by hand with the token:

```
curl -sS http://127.0.0.1:8090/metrics -H "Authorization: Bearer $VDE_METRICS_TOKEN"
```

Or use the bundled Prometheus, which reads the token from a compose secret
([`prometheus.yml`](prometheus.yml)):

```
docker compose --profile prometheus up -d      # then open http://localhost:9090
```

## Stopping

The `vde` service has `stop_grace_period: 40s`: on `docker compose stop` or `down` the server
stops accepting requests and drains in-flight requests and running jobs for up to 30 s (a job
still running then resumes after the next start). Docker's default of 10 s would kill it first.

## Data and cleanup

The database and any retained documents live in the `vde-data` volume and survive
`docker compose down`. `docker compose down -v` deletes them.

## Without compose

```
docker run --rm -p 127.0.0.1:8090:8090 -e ANTHROPIC_API_KEY \
  -v "$PWD/vde.yaml:/etc/vde/vde.yaml:ro" -v vde-data:/var/lib/vde \
  ghcr.io/kaanemec/vde:latest
```

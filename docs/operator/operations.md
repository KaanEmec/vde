# Operations

Running `vde` day to day: health checks, metrics, the audit log, logs, backups and upgrades.
Install is in [install](install.md); problems are in [troubleshooting](troubleshooting.md).

## What runs

`vde serve` is one process: the HTTP API, `worker.size` job workers (default 4), and the
sweeper that enforces [retention](retention.md) every `retention.sweep_interval` (default one
minute). State lives in the [data directory](install.md#the-data-directory), or in
PostgreSQL and S3 ([storage](storage.md)). Jobs are leased from the database: a job whose
worker died or whose process was killed is leased again once its lease passes
(`worker.lease`, default 2 minutes), and a job still `running` after its time limit plus one
minute is finished by the sweeper as `failed` with code `timeout`.

Shutdown on `SIGINT` or `SIGTERM` drains for up to 30 seconds. In Docker the default stop
timeout is 10 seconds, so set `stop_grace_period: 40s` (compose) or `docker stop -t 40` to
let running jobs finish; a job cut off resumes after the next start either way.

## Process roles

On PostgreSQL and S3 the parts of the service can run in separate processes, scaled
separately, with `vde serve --role`:

| `--role` | Runs | HTTP on `server.listen` |
| --- | --- | --- |
| `all` (default) | API, worker pool, sweeper | `/v1`, `/healthz`, `/metrics`; the admin API on `admin.listen` |
| `api` | API only: submissions are stored and wait for a worker | `/v1`, `/healthz` with `queue_age_seconds`, `/metrics`; the admin API on `admin.listen` |
| `worker` | worker pool and sweeper | `/healthz` and `/metrics` only; `admin.listen` is ignored |

All processes use the same configuration file. What they share and what they do not:

- **The queue.** Workers lease the oldest queued job with `FOR UPDATE SKIP LOCKED`, so no two
  take the same job, and write every attempt and the terminal state under their lease (the
  worker id is the host name, pid and a random suffix: containers all run as pid 1). A
  killed worker's job resumes on another process once its lease expires (`worker.lease`,
  2 minutes; renewed every 40 s while the job runs).
- **Wake-up.** A submission wakes a worker of its own process at once; other processes find it
  at their next poll, within `worker.poll_interval` (500 ms). There is no `LISTEN/NOTIFY`:
  the poll is one indexed query per idle worker and keeps the code path the same on SQLite.
- **The sweeper** runs on worker processes only, one at a time: each sweep takes a PostgreSQL
  advisory lock without waiting and is skipped while another process holds it. A process that
  dies loses its session and the lock with it. On SQLite the lock is always granted.
- **Webhooks** are sent by the worker process that finished the job; `POST /v1/extract` on an
  API-only process polls the job store for the result.
- **Per-process state.** Endpoint health (pairs marked down after failures) is per process and
  heals on its own; `vde_endpoint_up` exposes it with an `instance` label. Rate limits are per
  process unless `rate.shared: true` ([storage](storage.md#which-to-choose)).

A compose example with one API, two workers, PostgreSQL and RustFS is
[`examples/compose/scaled/`](../../examples/compose/scaled/README.md).

## Health: `/healthz`

`GET /healthz` is unauthenticated and not rate limited. It answers `200 {"status":"ok"}`, or
`503` with reason codes and the standard error object:

```json
{"status":"unavailable","reasons":["role_judge_down"],"error":{"code":"unavailable","message":"The service cannot accept jobs right now."}}
```

| Reason | Meaning |
| --- | --- |
| `db` | The one-row database query failed or took over 1 second. |
| `blobs` | A file cannot be created in `server.data_dir` (checked at most every 30 seconds). |
| `role_extractor_down` | Every pair of the extractor group, or of one of its escalation groups, is marked down after retryable failures. |
| `role_judge_down` | The same for the judge group, its escalation groups, the second judge or the feedback group. |

On an API-only process (`--role api`) no provider is called, so the role reasons are not
checked; the body carries `queue_age_seconds` instead, the age of the oldest queued job (0
when none waits): `{"status":"ok","queue_age_seconds":3}`. It never turns the answer into a
`503` (restarting the API does not bring workers back); alert on it instead, for example
above a minute.

A role recovers by itself: a down mark lasts 30 seconds, doubling to at most 5 minutes, and
the pair is still tried after the healthy ones. Reasons never name providers or documents.
Point the orchestrator at it, not the public internet. Behind `trusted_proxy`, a direct probe
needs the header `X-Forwarded-Proto: https`; `vde healthcheck` sends it. The container image
runs `vde healthcheck` every 30 seconds. `/healthz` checks that the service can accept jobs;
it does not make a model call, so a wrong provider key does not show here (see
[troubleshooting](troubleshooting.md#provider-failures)).

## Metrics

`GET /metrics` serves Prometheus text. Its labels are configuration ids and fixed words only;
never content.

| Series | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `vde_jobs_total` | counter | `app`, `status` | Jobs that reached `accepted`, `rejected`, `failed` or `extracted`. |
| `vde_job_attempts` | histogram | | Extractor attempts per terminal job. |
| `vde_job_duration_seconds` | histogram | `status` | Creation to terminal status, queue time included. |
| `vde_provider_calls_total` | counter | `role`, `group`, `endpoint`, `model`, `outcome` | Provider calls; `outcome` is `ok`, `retryable` or `permanent`. `group` tells escalation steps and the second judge apart. |
| `vde_provider_latency_seconds` | histogram | `role`, `group`, `endpoint` | Seconds per provider call. |
| `vde_provider_tokens_total` | counter | `role`, `group`, `endpoint`, `model`, `kind` | Tokens the providers reported; `kind` is `input` or `output`. The same counts are kept per job (admin API, `job.finished` audit events). |
| `vde_endpoint_up` | gauge | `instance`, `group`, `endpoint`, `model` | `1` while this process has not marked the pair down, `0` during its cooldown. Per process: `instance` is the process's id (host, pid, random suffix); Prometheus renames it `exported_instance` beside its own `instance` target label. Not on API-only processes. |
| `vde_failovers_total` | counter | `role`, `from`, `to` | Moves from one endpoint to the next in a group. |
| `vde_quota_remaining` | gauge | `app` | Submissions left in the current quota window. |
| `vde_jobs_in_progress` | gauge | `app` | Jobs queued or running. |
| `vde_http_requests_total` | counter | `route`, `status` | Requests by route pattern and status. |
| `vde_swept_jobs_total` | counter | | Jobs deleted by the sweeper. |
| `vde_judge_decisions_total` | counter | `policy`, `decision` | Finished jobs by judge policy (`always`, `never`, `auto`) and how their last schema-valid candidate was decided: `judged`, `grounded` (`auto`, accepted on the grounding check without a judge), `sampled` (`auto`, judged although grounded) or `skipped` (`never`). Jobs with no schema-valid candidate are not counted. |
| `vde_grounding_disagreements_total` | counter | | Sampled judge calls under `auto` that rejected a candidate the grounding check alone would have accepted (ADR 0009): the measured false-accept risk of `grounded`. |

`metrics.go_runtime: true` adds the Go runtime and process series.

Useful alerts: `/healthz` not `200` for a few minutes; a rising share of
`vde_jobs_total{status="failed"}`; `vde_provider_calls_total{outcome!="ok"}` growing for one
endpoint; `vde_quota_remaining` near zero for an application; `vde_jobs_in_progress` stuck at
`rate.max_in_progress`; `queue_age_seconds` of an API-only process above a minute (no worker
leases). Token spend per model: `sum by (model, kind) (rate(vde_provider_tokens_total[1h]))`.

### Protecting `/metrics`

Metrics reveal traffic volume per application, so they are protected. Exactly one mode
applies, chosen by the configuration:

| Configuration | Where `/metrics` is served | Who can read it |
| --- | --- | --- |
| Nothing set (default) | `server.listen` | Loopback peers only, and only requests without a forwarding header (`Forwarded`, `X-Forwarded-For`, `-Proto`, `-Host`, `X-Real-Ip`); anything else gets `404`. Behind a proxy on the same host this keeps it closed. |
| `metrics.token_env: NAME` | `server.listen` | Requests with `Authorization: Bearer <value of $NAME>`; others get `401`. The variable must be set. |
| `metrics.listen: host:port` | A second listener, `/metrics` only | With `token_env` set, the token; otherwise anyone who reaches the port. `/metrics` answers `404` on `server.listen`. A non-loopback address without a token is a `config check` warning. It must differ from `server.listen`. |

The metrics listener is plain HTTP and not subject to the HTTPS rule, so keep it on a
private network. For Prometheus in another container, use `metrics.token_env` (compose
example: [`prometheus.yml`](../../examples/compose/prometheus.yml)) or `metrics.listen` on a
private interface. Never route `/metrics` through the public reverse proxy; the Caddyfile
of the example answers `404` for it.

## Logs

Logs go to stderr, as JSON (`logging.format: json`, default) or `text`, at `logging.level`
(`info` by default). Collect them from the container or service manager.

At start the service logs `vde serve starting` (version, listen address, TLS and proxy
mode), one `application enabled` line per application, and warnings for the risky settings:
no applications configured, `server.insecure_http`, `debug.log_content`. Each request logs
one `request` line:

```json
{"time":"...","level":"INFO","msg":"request","method":"POST","route":"POST /v1/jobs","status":202,"duration_ms":41,"request_id":"r_01J...","app_id":"bookings"}
```

The paths never appear (only the route pattern), nor query strings, headers or bodies.
Callers can quote the `request_id` from an error response, and you find the matching lines
with it.

### The allow-list

The logger keeps a fixed vocabulary of attribute keys and **silently drops any other key**;
string values longer than 256 characters are cut. This is what keeps documents,
instructions, schemas, answers and credentials out of the logs, even after a coding mistake.
The keys are listed in [privacy](privacy.md#what-is-logged). If you build a log pipeline,
expect only those keys. Content can reach a log only through `debug.log_content`, and then
only into a separate file; see [privacy](privacy.md#the-debug-switch-debuglog_content).

## Audit log

The service records security-relevant events in the `audit_events` table of `vde.db`. Events
never contain document content, instructions, results or tokens.

| Kind | When | Actor | Details |
| --- | --- | --- | --- |
| `config.loaded` | `vde serve` starts | `config` | `service_version` |
| `app.enabled`, `app.disabled` | An application appears in or disappears from the configuration at start | `config` | none |
| `token.created` | `vde token create` | `cli` | `token_id`, `scopes`, `expires_at` |
| `token.revoked` | `vde token revoke` | `cli` | `token_id` |
| `auth.failed` | A request was refused | `api` | `reason` (see [applications and tokens](applications-and-tokens.md#what-the-caller-sees)) |
| `job.created` | A job was accepted | `api` | `media_type`, `bytes`, `pages`, `retention`, `service_version` |
| `job.finished` | A job reached a terminal status | `worker` | `status`, `attempts`, `error_code`, `service_version`, `input_tokens`, `output_tokens`, `verification` (`judge`, `grounded` or `none`), `judge_policy` |
| `job.deleted` | Content deleted before expiry | `api` | `reason` (`request` or `first_read`), `retention` |
| `job.expired` | The sweeper removed a job | `sweeper` | `status`, `retention` |
| `webhook.delivered` | A completion webhook was answered `2xx` | `webhook` | `event`, `attempt`, `status_code` |
| `webhook.failed` | A webhook attempt failed (one row per attempt) | `webhook` | `event`, `attempt`, `reason` (`status`, `network` or `queue_full`), `status_code`, `final` (no retry follows) |

Each row has `at` (Unix milliseconds), `kind`, `application_id`, `actor`, `job_id` and
`details` (JSON). Read them with `sqlite3`, read-only, while the service runs:

```
sqlite3 -readonly /var/lib/vde/vde.db \
  "SELECT datetime(at/1000,'unixepoch'), kind, application_id, actor, job_id, details
   FROM audit_events WHERE application_id = 'bookings' ORDER BY id DESC LIMIT 50"
```

`vde audit list [--config path] [--app id] [--since duration|RFC3339] [--kind kind] [--limit n]`
prints the same rows without a database client.

| Key | Default | Meaning |
| --- | --- | --- |
| `audit.log` | `false` | Also write each event the service records as an `info` log line with `audit=true`, `kind`, `application_id`, `job_id`, `actor` and `details`, so a log collector can ship them. Events of `vde token create` and `vde token revoke`, and `app.enabled` and `app.disabled`, go only to the table. |
| `audit.retention` | `8760h` (365 days) | How long events are kept; at least `24h`. The sweeper deletes older rows. |

The audit rows are in the same SQLite file as everything else and are not tamper-evident;
ship the log stream (`audit.log: true`) to an external system if you need that. See
[security](../security.md).

## Backups

This section is about the defaults (SQLite and local blobs); for PostgreSQL and S3 see
[storage](storage.md#backups). State is the SQLite database and the blobs directory, both under `server.data_dir`. The
configuration file and the provider keys are yours to back up separately. What the database
holds is sensitive (tokens' hashes, instructions, results of unexpired jobs), so store
backups as protected as the live volume.

What you lose without a backup: applications are recreated from the configuration at the
next start, but **tokens are not** (create new ones and redistribute them) and jobs in
flight, results not yet read, and the audit log are gone.

### Hot backup (service running)

Use SQLite's online backup, which is safe while the service writes; do not copy `vde.db`
alone with `cp`, since the write-ahead log (`vde.db-wal`) holds recent changes.

```
sqlite3 /var/lib/vde/vde.db ".backup '/backups/vde-$(date +%F).db'"
rsync -a /var/lib/vde/blobs/ /backups/blobs/
```

Take the database first and the blobs after. The two are not one atomic snapshot: a job
created in between may have blobs without a row (the sweeper removes them) or the reverse
(that job cannot run). For short-lived `retention: none` data this rarely matters.

`sqlite3` is not in the container image. Run it on the host against the volume's directory
(`docker volume inspect vde-data` shows it), or from a helper container that has it.

### Cold backup (service stopped)

Stop `vde serve`, copy the whole data directory (`vde.db`, `vde.db-wal` and `vde.db-shm` if
present, and `blobs/`), start it again. This is the simplest consistent backup. With
snapshots (LVM, ZFS, a cloud disk) a volume snapshot taken while running is also safe, since
SQLite recovers from the write-ahead log.

### Restore

Stop the service, replace the data directory contents with the backup (database file and
`blobs/` from the same moment), make the files owned by the service user (uid 65532 in the
container) and start. Check `vde app list`, `vde token list --app <id>` and `/healthz`.

## Upgrades and migrations

1. Read the [changelog](../../CHANGELOG.md) for the new version.
2. Back up (above).
3. Check your configuration with the **new** binary: `vde config check --config vde.yaml`
   (`--strict` in automation). New releases may add keys and warnings.
4. Stop the old process, start the new one (pull the image and recreate the container, or
   replace the binary). There is no rolling upgrade: one process owns the data directory.
5. Confirm `vde version`, the `vde serve starting` log line and `/healthz`.

**Migrations run at start.** `vde serve` (and the `token` and `app` commands, which open the
same database) applies pending schema migrations in order, each in its own transaction, and
records them in the `schema_migrations` table. There is nothing to run by hand; `vde migrate`
applies them explicitly when you prefer that (see [storage](storage.md#migrations)).

**Downgrades are unsupported.** Migrations only move forward and the service does not check
whether the database is newer than the binary, so an older binary on a migrated database may
fail or misbehave. To go back, restore the backup taken before the upgrade, then run the
older version.

The pre-1.0 `vde` versions may change the configuration between minor versions; the
changelog says so when they do.

### Reclaiming space

Deleting jobs does not shrink the file. If `vde.db` grows large after heavy use, run
`VACUUM` manually in a quiet moment: stop the service, or run
`sqlite3 /var/lib/vde/vde.db 'VACUUM'` while it is idle, with free disk space equal to the
database size. The service never vacuums on its own.

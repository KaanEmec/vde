# Configuration reference

Part of the [operator guide](README.md). Every key is listed with its type, default and
meaning; the example files are explained [at the end](#the-example-files).

`vde` reads one YAML file (`--config`, default `vde.yaml`). Unknown keys are errors.
Secrets never appear in the file: an endpoint names the environment variable that holds its
key (`api_key_env`), and nothing else in the file is interpolated from the environment.
Durations are strings such as `90s`, `3m` or `1h30m`; a bare number is rejected. Inside
`endpoints`, `groups` and `applications`, a zero or omitted value means the default.
Run `vde config check` to validate a file without starting the service. The file is read once
at start: restart `vde serve` after editing it or after changing a key variable. Keys that
the operator commands (`vde token`, `vde app`, `vde healthcheck`) read are `server`, `auth`
and `applications`; they decode the file but do not validate it. Messages of the check are
explained in [troubleshooting](troubleshooting.md#config-check-messages).
`vde config check --probe` is accepted but not implemented yet: no provider has a probe, so it
only warns "probe not available" for each endpoint and checks nothing more. Reachability
probing is planned for v0.3; until then test an endpoint with `vde extract` and a small document.

## `server` (used from v0.2)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `server.listen` | string | `127.0.0.1:8090` | Listen address, `host:port` (`:8090` for all interfaces, as in a container). Plain HTTP on a non-loopback address needs `tls`, `trusted_proxy` or `insecure_http`; see [install](install.md#tls-or-a-reverse-proxy). |
| `server.tls.cert_file` | string | empty | TLS certificate; set together with `key_file` to terminate TLS in the binary. |
| `server.tls.key_file` | string | empty | TLS private key. |
| `server.trusted_proxy` | bool | `false` | A reverse proxy terminates TLS; requests must carry `X-Forwarded-Proto: https`, from loopback too, or answer `400`. The header is believed from any peer, so make the service reachable only through the proxy. |
| `server.insecure_http` | bool | `false` | Accept plain HTTP from any peer on any listener, for development or containers on a trusted private network only; `serve` logs a warning at start and `config check` warns. Not together with `tls`. |
| `server.data_dir` | string | `/var/lib/vde` | Directory for the database (`vde.db`, shared by `serve`, `vde token` and `vde app`) and blobs (`blobs/`); also `content.log` when `debug.log_content` is on. Created with mode `0700` when missing. See [install](install.md#the-data-directory). |

## `endpoints.<name>`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `provider` | string | required | `anthropic`, `openai_compatible`, `gemini` or `fake` (tests). |
| `preset` | string | empty | `openrouter` (only with `openai_compatible`): sets the base URL and attribution headers. |
| `base_url` | string | per provider | `https://api.anthropic.com`, `https://generativelanguage.googleapis.com`, the preset's URL; required for `openai_compatible` without a preset. |
| `api_key_env` | string | required except `openai_compatible` and `fake` | Name of the environment variable holding the key. The variable must be set when named. |
| `timeout` | duration | `90s` | Per-call timeout of one provider request; must be positive. Local models often need more. |
| `models.<model>.accepts` | list of media types | required, non-empty | Media types the model reads natively: `application/pdf`, `image/png`, `image/jpeg`, `image/webp`, `image/heic`, `text/plain`, `text/html`, `text/markdown`. At least one model per endpoint. You declare it; `vde` never probes the model. See [providers](providers.md#declaring-accepts). |

## `groups.<name>`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `endpoints` | list | required, non-empty | Ordered failover list of `{endpoint, model}`; the model must be declared on the endpoint. |
| `failover.max_tries` | int | `3` | Tries across the list for one role call. |
| `failover.max_total_time` | duration | `3m` | Upper bound on the time spent on one role call including failover; must be less than `limits.job_timeout`. |
| `failover.failover_on_permanent` | bool | `false` | Try the next pair after a permanent failure (refusal, bad request) instead of failing the call at once. |

## `roles`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `roles.extractor.group` | string | required | Group serving the extractor. |
| `roles.judge.group` | string | required | Group serving the judge; may be the extractor's group. |

## `limits`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `limits.max_attempts` | int | `3` | Default attempt limit per application; a request can only lower it. At least 1. |
| `limits.job_timeout` | duration | `5m` | Deadline per job, counted from its creation, so time spent queued counts; positive and at most `8m`. Expiry fails the job with `timeout`. |
| `limits.judge_reparse_tries` | int | `1` | Extra judge calls when its verdict cannot be parsed; `0` or more. When all fail the job ends `judge_failed`. |
| `limits.feedback_message_chars` | int | `4000` (when `0` or omitted) | Characters kept per feedback message, the last replaced by an ellipsis; the contract asks for at least 2,000. |
| `limits.document_bytes` | int | `26214400` (25 MiB) | Largest document; over it `413`. Positive; below the contract minimum is a warning. |
| `limits.page_bytes` | int | `10485760` (10 MiB) | Largest page image; below the contract minimum is a warning. |
| `limits.pages` | int | `20` | Most page images per job; below the contract minimum is a warning. |
| `limits.request_bytes` | int | `134217728` (128 MiB) | Largest whole `POST /v1/jobs` body; over it the service reads up to the limit and answers `413`; below the contract minimum is a warning. |

## `documents`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `documents.html` | string | `raw_html` | HTML representation: `raw_html` or `visible_text`. |

## `applications[]` (used from v0.2)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `id` | string | required, unique | Application id; the key of its tokens, jobs and metrics labels. |
| `name` | string | empty | Display name. |
| `max_attempts` | int | `limits.max_attempts` | Attempt limit for the application's jobs; above `limits.max_attempts` is a warning. Part of the application's `service_version`. |
| `quota.limit` | int | `1000` | Submissions per window. |
| `quota.window` | string | `P1D` | ISO 8601 duration of the quota window in weeks, days, hours, minutes or seconds (`P1D`, `PT1H`, `P7D`); years and months are refused. |
| `rate.requests_per_minute` | int | `300` | Requests per minute across `/v1`. Rate limits are in memory, per process. |
| `rate.submits_per_minute` | int | `30` | Job submissions per minute. |
| `rate.max_in_progress` | int | `20` | Jobs queued or running at once. |
| `retention.default` | string | `24h` | Retention for `retention: default` jobs: a positive duration (`24h`, `72h`) or `none`. |
| `retention.force_none` | bool | `false` | Treat every job as `retention: none`. |
| `retention.delete_blobs_at_terminal` | bool | `false` | Delete the document and pages of `retention: default` jobs when they reach a terminal state instead of at expiry; the result stays readable. See [retention](retention.md). |
| `documents.html` | string | top-level `documents.html` | Per-application HTML policy override: `raw_html` or `visible_text`. |

Applications are mirrored into the database when `vde serve` starts: an application
removed from the file is disabled, its tokens stop working, and its jobs remain until they
expire. Applications created over the [admin API](../api/admin.md) are kept as stored; for
the applications the file declares, the file wins at every start. `vde app list` shows
them. All counts above (`max_attempts`, `quota.limit`, `rate.*`) must be at least 1. See [applications and tokens](applications-and-tokens.md).

## `auth`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `auth.max_token_ttl` | duration | `17520h` (730 days) | Longest lifetime `vde token create --expires` accepts; every token expires. |

## `logging` and `debug`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `logging.level` | string | `info` | `debug`, `info`, `warn` or `error`. What is logged: [privacy](privacy.md). |
| `logging.format` | string | `json` | `json` or `text`. |
| `debug.log_content` | bool | `false` | Log document content to `content.log` in `server.data_dir` (never stdout), for local development only; `serve` warns at start; refused with a non-loopback `server.listen`. |

## `worker`, `retention`, `audit` and `metrics` (used by `vde serve`)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `worker.size` | int | `4` | Jobs run at once by the worker pool, 1 to 256. Also the most concurrent provider calls the service starts. |
| `worker.poll_interval` | duration | `500ms` | Sleep of an idle worker between looks at the queue (a submission wakes one at once); at least `10ms`. |
| `retention.sweep_interval` | duration | `1m` | Time between expiry sweeps (expired jobs, stale running jobs, old quota, token and audit rows); at least `1s`. |
| `audit.log` | bool | `false` | Also write each audit event the service records as an info log line with `audit=true` (never content). The events are always stored in the database. See [operations](operations.md#audit-log). |
| `audit.retention` | duration | `8760h` (365 days) | How long audit events are kept; at least `24h`. |
| `metrics.token_env` | string | empty | Name of an environment variable holding a bearer token that `GET /metrics` requires; the variable must be set when named. |
| `metrics.listen` | string | empty | A second listener, `host:port`, serving only `/metrics` (with the token when `token_env` is set); `/metrics` then answers `404` on `server.listen`. Not loopback and no token is a warning. With neither key, `/metrics` on `server.listen` answers only loopback peers that did not come through a proxy. |
| `metrics.go_runtime` | bool | `false` | Add the Go runtime and process series to `/metrics`. The series are listed in [operations](operations.md#metrics). |
| `admin.listen` | string | `127.0.0.1:8091` | The admin API's own listener (`/admin/v1`, admin tokens from `vde token create --admin` only; see [the admin API](../api/admin.md)). Empty disables it. Must differ from `server.listen` and `metrics.listen`; a non-loopback address is refused unless `server.tls` is set (the admin listener serves the same certificate) or `server.insecure_http` is true. `server.trusted_proxy` does not apply to it. |

`GET /healthz` is unauthenticated and not rate limited: it runs a one-row database query
(1 s timeout), reads the role groups' in-memory state and checks the data directory is
writable at most every 30 s. Expose it to the orchestrator, not the public internet. Its reasons are explained in
[operations](operations.md#health-healthz).

## The example files

[`examples/`](../../examples/README.md) holds complete, valid configurations. Copy one, set
the variable it names and run `vde config check --config <file>`. In every file `accepts` is
your declaration of what each model reads (`vde` never probes a model), the model names were
current when the file was written (use ones your account can call), and the key itself is
never in the file.

| File | Use it for | What to know |
| --- | --- | --- |
| [`examples/vde.dev.yaml`](../../examples/vde.dev.yaml) | The smallest working setup: one Anthropic endpoint, one model, one group serving both roles. | Needs `ANTHROPIC_API_KEY`. Sets `server.data_dir: ./vde-data` so it runs without root, and listens on the default loopback address. It has no `applications`, so `vde serve` would refuse every `/v1` request: add one (see the compose file) before creating tokens. The same model judges its own work; fine for trying, not for real use. |
| [`examples/compose/vde.yaml`](../../examples/compose/vde.yaml) | A running service in Docker: the dev setup plus `server` and one application, `dev-app`. | `listen: ":8090"` because the container must listen on all interfaces; `insecure_http: true` because requests arrive over the compose network and the host port is published on loopback only (development). For TLS, set `insecure_http: false` and `trusted_proxy: true` behind the commented Caddy service. `data_dir: /var/lib/vde` is the named volume. Walk-through: [compose README](../../examples/compose/README.md). |
| [`examples/vde.failover.yaml`](../../examples/vde.failover.yaml) | Production-like layout: primary and fallback per role across Anthropic, Gemini and OpenRouter. | Needs all three keys. The judge group uses different models than the extractor group. `failover.max_total_time` stays below `limits.job_timeout`. Every endpoint in a group may receive the document, so approve each. |

Other files: [`vde.gemini.yaml`](../../examples/vde.gemini.yaml) (Google Gemini; use a
paid-tier key for confidential documents), [`vde.openrouter.yaml`](../../examples/vde.openrouter.yaml)
(the OpenRouter preset) and [`vde.local-ollama.yaml`](../../examples/vde.local-ollama.yaml)
(fully local, no key; no PDF reader, so `config check` warns that PDFs need caller-supplied
page images).

The service keys not shown in the examples (`limits`, `worker`, `retention`, `audit`,
`metrics`, `auth`, `logging`) all have defaults and are only set when you need to change
them; the tables above are the complete list.

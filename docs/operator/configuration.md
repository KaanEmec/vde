# Configuration reference

Status: reference table for v0.1; the full operator guide is v0.2 epic 10.

`vde` reads one YAML file (`--config`, default `vde.yaml`). Unknown keys are errors.
Secrets never appear in the file: an endpoint names the environment variable that holds its
key (`api_key_env`), and nothing else in the file is interpolated from the environment.
Durations are strings such as `90s`, `3m` or `1h30m`; a bare number is rejected. Inside
`endpoints`, `groups` and `applications`, a zero or omitted value means the default.
Run `vde config check` to validate a file without starting the service.

## `server` (used from v0.2)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `server.listen` | string | `127.0.0.1:8090` | Listen address, `host:port`. Plain HTTP on a non-loopback address needs `tls`, `trusted_proxy` or `insecure_http`. |
| `server.tls.cert_file` | string | empty | TLS certificate; set together with `key_file` to terminate TLS in the binary. |
| `server.tls.key_file` | string | empty | TLS private key. |
| `server.trusted_proxy` | bool | `false` | A reverse proxy terminates TLS; requests must carry `X-Forwarded-Proto: https`. |
| `server.insecure_http` | bool | `false` | Accept plain HTTP from any peer on any listener, for development or containers on a trusted private network only; `serve` logs a warning at start and `config check` warns. Not together with `tls`. |
| `server.data_dir` | string | `/var/lib/vde` | Directory for the database (`vde.db`, shared by `serve`, `vde token` and `vde app`) and blobs. |

## `endpoints.<name>`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `provider` | string | required | `anthropic`, `openai_compatible`, `gemini` or `fake` (tests). |
| `preset` | string | empty | `openrouter` (only with `openai_compatible`): sets the base URL and attribution headers. |
| `base_url` | string | per provider | `https://api.anthropic.com`, `https://generativelanguage.googleapis.com`, the preset's URL; required for `openai_compatible` without a preset. |
| `api_key_env` | string | required except `openai_compatible` and `fake` | Name of the environment variable holding the key. The variable must be set when named. |
| `timeout` | duration | `90s` | Per-call timeout. |
| `models.<model>.accepts` | list of media types | required, non-empty | Media types the model reads natively: `application/pdf`, `image/png`, `image/jpeg`, `image/webp`, `image/heic`, `text/plain`, `text/html`, `text/markdown`. |

## `groups.<name>`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `endpoints` | list | required, non-empty | Ordered failover list of `{endpoint, model}`; the model must be declared on the endpoint. |
| `failover.max_tries` | int | `3` | Tries across the list for one role call. |
| `failover.max_total_time` | duration | `3m` | Upper bound on the time spent on one role call including failover. |
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
| `limits.job_timeout` | duration | `5m` | Deadline per job; at most `8m`. |
| `limits.judge_reparse_tries` | int | `1` | Extra judge calls when its verdict cannot be parsed. |
| `limits.feedback_message_chars` | int | `4000` (when `0` or omitted) | Characters kept per feedback message, the last replaced by an ellipsis; the contract asks for at least 2,000. |
| `limits.document_bytes` | int | `26214400` (25 MiB) | Largest document; below the contract minimum is a warning. |
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
| `id` | string | required, unique | Application id. |
| `name` | string | empty | Display name. |
| `max_attempts` | int | `limits.max_attempts` | Attempt limit for the application's jobs; above `limits.max_attempts` is a warning. Part of the application's `service_version`. |
| `quota.limit` | int | `1000` | Submissions per window. |
| `quota.window` | string | `P1D` | ISO 8601 duration of the quota window in weeks, days, hours, minutes or seconds (`P1D`, `PT1H`, `P7D`); years and months are refused. |
| `rate.requests_per_minute` | int | `300` | Requests per minute across `/v1`. |
| `rate.submits_per_minute` | int | `30` | Job submissions per minute. |
| `rate.max_in_progress` | int | `20` | Jobs queued or running at once. |
| `retention.default` | string | `24h` | Retention for `retention: default` jobs: a duration or `none`. |
| `retention.force_none` | bool | `false` | Treat every job as `retention: none`. |
| `retention.delete_blobs_at_terminal` | bool | `false` | Delete the document and pages of `retention: default` jobs when they reach a terminal state instead of at expiry; the result stays readable. See [retention](retention.md). |
| `documents.html` | string | `documents.html` | Per-application HTML policy override. |

Applications are mirrored into the database when `vde serve` starts: an application
removed from the file is disabled, its tokens stop working, and its jobs remain until they
expire. `vde app list` shows them.

## `auth`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `auth.max_token_ttl` | duration | `17520h` (730 days) | Longest lifetime `vde token create --expires` accepts; every token expires. |

## `logging` and `debug`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `logging.level` | string | `info` | `debug`, `info`, `warn` or `error`. |
| `logging.format` | string | `json` | `json` or `text`. |
| `debug.log_content` | bool | `false` | Log document content to `content.log` in `server.data_dir` (never stdout), for local development only; `serve` warns at start; refused with a non-loopback `server.listen`. |

## `worker`, `retention`, `audit` and `metrics` (used by `vde serve`)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `worker.size` | int | `4` | Jobs run at once by the worker pool, 1 to 256. |
| `worker.poll_interval` | duration | `500ms` | Sleep of an idle worker between looks at the queue (a submission wakes one at once); at least `10ms`. |
| `retention.sweep_interval` | duration | `1m` | Time between expiry sweeps (expired jobs, stale running jobs, old quota, token and audit rows); at least `1s`. |
| `audit.log` | bool | `false` | Also write each audit event as an info log line with `audit=true` (never content). |
| `audit.retention` | duration | `8760h` (365 days) | How long audit events are kept; at least `24h`. |
| `metrics.token_env` | string | empty | Name of an environment variable holding a bearer token that `GET /metrics` requires; the variable must be set when named. |
| `metrics.listen` | string | empty | A second listener, `host:port`, serving only `/metrics` (with the token when `token_env` is set); `/metrics` then answers `404` on `server.listen`. Not loopback and no token is a warning. With neither key, `/metrics` on `server.listen` answers only loopback peers that did not come through a proxy. |
| `metrics.go_runtime` | bool | `false` | Add the Go runtime and process series to `/metrics`. |

`GET /healthz` is unauthenticated and not rate limited: it runs a one-row database query
(1 s timeout), reads the role groups' in-memory state and checks the data directory is
writable at most every 30 s. Expose it to the orchestrator, not the public internet.

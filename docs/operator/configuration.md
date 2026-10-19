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
`vde config check` makes no network call; to test an endpoint, run `vde extract` with a small
document.

## Schema

The keys of 1.x are described by a JSON Schema (draft 2020-12) built into the binary;
`vde config schema` prints it. For completion and inline errors in an editor, save it next
to the file with `vde config schema > vde.schema.json` and make
`# yaml-language-server: $schema=./vde.schema.json` the file's first line; VS Code's YAML
extension and other editors using yaml-language-server read that comment.
Save the schema again after upgrading `vde`. `vde config check` validates the file against
the same schema after its own checks and reports each violation as
`error: schema: <key>: <message>`; any violation fails the check. The schema checks key
names, types, accepted values, ranges and the form of durations; rules across keys (a
group's model declared on its endpoint, `failover.max_total_time` below
`limits.job_timeout`, a named variable being set) are only in `config check`'s own
validation, which is also all that `serve` and `extract` run. A key with no value
(`server:`) counts as absent, as it does for `vde`.

**Changes within 1.x.** The keys of 1.0 are the keys of every 1.x release. A new key
arrives with a default that reproduces the previous behaviour. If a key ever has to be
renamed, the old name keeps working for one minor version, with a warning from
`vde config check` and `vde serve` that names the replacement, and is removed in the next;
the CHANGELOG lists it under "Deprecated". No key is deprecated in 1.0, and a 0.x file
needs no rewrite: no key changed on the way to 1.0, so there is no migration command.

## `server` (used from v0.2)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `server.listen` | string | `127.0.0.1:8090` | Listen address, `host:port` (`:8090` for all interfaces, as in a container). Plain HTTP on a non-loopback address needs `tls`, `trusted_proxy` or `insecure_http`; see [install](install.md#tls-or-a-reverse-proxy). |
| `server.tls.cert_file` | string | empty | TLS certificate; set together with `key_file` to terminate TLS in the binary. |
| `server.tls.key_file` | string | empty | TLS private key. |
| `server.trusted_proxy` | bool | `false` | A reverse proxy terminates TLS; requests must carry `X-Forwarded-Proto: https`, from loopback too, or answer `400`. The header is believed from any peer, so make the service reachable only through the proxy. |
| `server.insecure_http` | bool | `false` | Accept plain HTTP from any peer on any listener, for development or containers on a trusted private network only; `serve` logs a warning at start and `config check` warns. Not together with `tls`. |
| `server.data_dir` | string | `/var/lib/vde` | Directory for the database (`vde.db`, shared by `serve`, `vde token` and `vde app`; not used with `storage.driver: postgres`) and blobs (`blobs/`; not used with `blobs.driver: s3`). Created with mode `0700` when missing. See [install](install.md#the-data-directory). |

## `endpoints.<name>`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `provider` | string | required | `anthropic`, `openai_compatible`, `gemini`, `bedrock`, `vertex`, `classifier` (judge only, see [classifier](../providers/classifier.md)) or `fake` (tests). |
| `preset` | string | empty | `openrouter` (only with `openai_compatible`): sets the base URL and attribution headers. |
| `base_url` | string | per provider | `https://api.anthropic.com`, `https://generativelanguage.googleapis.com`, the preset's URL; required for `openai_compatible` without a preset. Optional for `bedrock` and `vertex`, where it replaces the service endpoint (a private endpoint). Plain `http://` on a non-loopback host is a warning: documents and the API key would cross the network unencrypted. |
| `api_key_env` | string | required except `openai_compatible`, `bedrock`, `vertex`, `classifier` and `fake` | Name of the environment variable holding the key. The variable must be set when named. Rejected for `bedrock` and `vertex`, which use the cloud's default credential chain. |
| `region` | string | required for `bedrock` and `vertex` | Cloud region that serves the calls (`us-east-1`, `us-east5`; `global` for Vertex). Not allowed on other providers. See [Bedrock](../providers/bedrock.md), [Vertex AI](../providers/vertex.md). |
| `project` | string | required for `vertex` | Google Cloud project id. Not allowed on other providers. |
| `timeout` | duration | `90s` | Per-call timeout of one provider request; must be positive. Local models often need more. |
| `models.<model>.accepts` | list of media types | required, non-empty | Media types the model reads natively: `application/pdf`, `image/png`, `image/jpeg`, `image/webp`, `image/heic`, `text/plain`, `text/html`, `text/markdown`. At least one model per endpoint. You declare it; `vde` never probes the model. See [providers](providers.md#declaring-accepts). |
| `models.<model>.max_output_tokens` | integer | `16384` | Output-token limit sent with every call to this model (`max_tokens`, `maxOutputTokens`). Raise it for large documents in schema-less mode; an answer cut off at it is retried with a request for a more compact one. Must be positive when set. |
| `models.<model>.questions` | list | required for `classifier`, not allowed otherwise | The classifier's fixed questions, `{id, text, threshold}`, each phrased so "yes" passes; `id` unique, `threshold` above 0 and at most 1. The verdict is `accept` when every probability reaches its threshold. A classifier model's `accepts` may list text types only. Part of `service_version`. |

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
| `roles.extractor.group` | string | required | Group serving the extractor. Must have no `classifier` endpoint. |
| `roles.judge.group` | string | required | Group serving the judge; may be the extractor's group. |
| `roles.extractor.escalation`, `roles.judge.escalation` | list of `{from_attempt, group}` | empty | Serve the role from `group` from attempt `from_attempt` on (a stronger model for later attempts); failover stays inside each group. `from_attempt` starts at 2, strictly increases and is at most `limits.max_attempts`. A resumed job counts attempts on, so it escalates on time. Every group a role may use must read the document's representation, or the submission is refused with `unsupported_document`. Invisible to callers except through `service_version`, which hashes the steps. |
| `roles.second_judge.group` | string | empty (off) | A second, independent judge with the same request: an `accept` of the judge is confirmed by it before the job is accepted, and its `reject` feeds the next attempt like the judge's. Its failures follow the judge's rules (`judge_failed`, `provider_unavailable`; never acceptance). On acceptance the advisory entries of both judges are kept and `judge_confidence` is the second judge's. Hashed into `service_version` when set; unset changes nothing. Calls are counted as `judge` calls in metrics and `--verbose`. |
| `roles.judge.feedback_fallback` | string | `whole_result` | What a reject from a `classifier` endpoint carries: `whole_result`, one entry at pointer `""` with a fixed message and the confidence; or `llm`, pointer-level feedback asked from `feedback_group` (one extra call per rejection). See [classifier](../providers/classifier.md#how-decisions-become-a-verdict). `llm` is hashed into `service_version`; a warning when no judge group has a classifier. |
| `roles.judge.feedback_group` | string | required with `feedback_fallback: llm`, not allowed otherwise | The LLM group asked for feedback on classifier rejections, with the judge framing; no `classifier` endpoint. |
| `roles.judge.policy` | string | `always` | When the judge is asked (v0.4, [judge policy](judge-policy.md), [ADR 0009](../architecture/decisions/0009-judge-policy-and-grounding.md)): `always` (contract v1: `accepted` means the judge accepted), `never` (a schema-valid final candidate ends `extracted`, never `accepted`) or `auto` (a grounded candidate may be accepted without a judge, with `verification: grounded`). The default applications inherit; `applications[].judge.policy` overrides it. `never` or `auto` is hashed into `service_version`; `always` changes nothing. `never` together with `roles.second_judge` is a warning; `roles.extractor.policy` is an error. |

## `limits`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `limits.max_attempts` | int | `3` | Default attempt limit per application; a request can only lower it. At least 1. |
| `limits.job_timeout` | duration | `5m` | Deadline per job, counted from its creation, so time spent queued counts; positive and at most `8m`. Expiry fails the job with `timeout`. |
| `limits.judge_reparse_tries` | int | `1` | Extra judge calls when its verdict cannot be parsed; `0` or more. When all fail the job ends `judge_failed`. |
| `limits.feedback_message_chars` | int | `4000` (when `0` or omitted) | Characters kept per feedback message, the last replaced by an ellipsis; the contract asks for at least 2,000. |
| `limits.document_bytes` | int | `26214400` (25 MiB) | Largest document; over it `413`. Positive; below the contract minimum is a warning. |
| `limits.page_bytes` | int | `10485760` (10 MiB) | Largest page image; below the contract minimum is a warning. |
| `limits.pages` | int | `20` | Most page images per job; below the contract minimum is a warning; at most `999` (stored page names have three digits). |
| `limits.request_bytes` | int | `134217728` (128 MiB) | Largest whole `POST /v1/jobs` body; over it the service reads up to the limit and answers `413`; below the contract minimum is a warning. |
| `limits.max_queued_jobs` | int | `0` (no ceiling) | Global ceiling on `queued` jobs, all applications together (v1.0). At or above it `POST /v1/jobs` and `POST /v1/extract` answer `503 unavailable` with `Retry-After: 5`, before any part is kept (the body is drained, not buffered), and charge no quota; a request with an `Idempotency-Key` is read first and refused only when the key is unused (a replay answers `202`); polls are unaffected. The count is reused for one second per process, so under a flood the ceiling can be passed by what other processes accept in that second. Not negative. Size it as `worker.size` × worker processes × (`limits.job_timeout` ÷ a typical job's duration): more queued jobs would time out before a worker leases them; see [operations](operations.md#memory-and-backpressure). |
| `limits.max_submits_in_flight` | int | `16` | Submissions (`POST /v1/jobs`, `POST /v1/extract`, multipart and JSON) one process reads and prepares at a time (v1.0). Each holds up to `limits.request_bytes` in memory while it is read, more on the JSON path (base64 and decoded bytes) and while a PDF or image is prepared. One application holds at most half of them (at least one), so its slow uploads cannot starve the others; over either limit a submission answers `503 unavailable` with `Retry-After: 5` before any part is read (the body is drained, not buffered) and charges no quota. At least 1. Polls and other routes are unaffected; see [operations](operations.md#memory-and-backpressure). |

## `documents`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `documents.html` | string | `raw_html` | HTML representation: `raw_html` or `visible_text`. |
| `documents.images.enabled` | bool | `true` | Prepare images once at submit, for both roles: downscale, apply EXIF orientation, convert WebP. `false` sends images exactly as received. |
| `documents.images.max_dimension` | int | `2048` | Longest side in pixels; larger images (and page images) are scaled down, keeping the aspect ratio, to save tokens. 64 to 16384. |
| `documents.images.convert_to` | string | `image/jpeg` | Type a converted image is encoded as: `image/jpeg` or `image/png`. A PNG or JPEG that is within `max_dimension` and needs no rotation is never re-encoded. |
| `documents.images.quality` | int | `85` | JPEG quality, 1 to 100. Ignored for `image/png`. |
| `documents.pdf.mode` | string | `auto` | How a PDF reaches groups whose models do not accept `application/pdf` (v0.4, ADR 0010): `auto` sends the PDF's text layer as text when every page has at least `min_text_chars_per_page` characters, else renders its pages; `text` and `pages` force one of the two; `native` renders nothing, so such a PDF needs caller `page` parts or is refused (`415`). A PDF-capable group still gets the PDF as given, and caller pages still win over rendering. See [PDF preprocessing](pdf.md). |
| `documents.pdf.dpi` | int | `150` | Resolution of rendered pages, 72 to 300. An A4 page at 150 dpi is 1240 × 1755 pixels; rendered pages then go through `documents.images`. |
| `documents.pdf.max_pages` | int | `20` | A PDF with more pages is refused (`415 unsupported_document`) unless it is sent as given to a PDF-capable group. 1 to 200. |
| `documents.pdf.min_text_chars_per_page` | int | `200` | Under `auto`, the characters (after trimming whitespace) every page needs for the PDF to go as text. A PDF's text layer is used for grounding when its pages average at least this many. 0 or more. |
| `documents.pdf.workers` | int | `1` | pdfium instances per process, 1 to 16. A memory decision: each takes about 230 to 490 MB while it works. Not part of `service_version`. |
| `documents.fetch.enabled` | bool | `false` | Let JSON submissions name their document by `url` (v0.3). Off by default: a fetch is an outbound request the caller triggers (SSRF risk); turn it on only for hosts you trust. |
| `documents.fetch.allowed_hosts` | list | empty | Exact host names (or `host:port` for a non-default port) the service may fetch from; required when enabled. Only `https`; redirects only to the same host; hosts resolving to loopback, private, link-local, shared or NAT64 addresses are refused at connect time. |
| `documents.fetch.max_bytes` | int | `limits.document_bytes` | Largest fetched document; never above `limits.document_bytes`. Over it `413`. |
| `documents.fetch.timeout` | duration | `10s` | Whole fetch, connect to last byte; positive and at most `1m`. |

The image policy is part of `service_version`, so changing it changes the version of
every result. A JPEG is rotated upright from its EXIF orientation tag. `image/webp` is
decoded in the service and converted when a group's models do not accept it, so an
endpoint that accepts only JPEG and PNG can still process a WebP document. `image/heic`
is not converted (there is no cgo-free HEIC decoder): it is accepted only when the
models of both groups declare `image/heic`. Images above 120 megapixels are refused with `413`.

`documents.pdf.mode`, `dpi` and `min_text_chars_per_page` are part of `service_version`
when a role's groups do not all read PDFs natively (or the judge policy is not `always`);
`max_pages` and `workers` are not. pdfium starts on the first PDF that needs it, so a
deployment whose models all read PDFs pays nothing at run time.

**Memory budget.** Each pdfium instance takes about 230 to 490 MB while it prepares a
document (its WebAssembly memory is capped at 1 GiB, and a fresh instance is used per
document, so the memory is returned afterwards). Budget about 500 MB per
`documents.pdf.workers` on top of the service; `config check` prints the estimate.
**Compilation cache.** The compiled module is cached under `server.data_dir/pdfium-cache`
(about 20 MB; the CLI uses `vde/pdfium-cache` under the user cache directory). Without a
writable cache the service logs a warning and compiles at every start (about 1.2 s instead
of 35 ms). **Fonts.** The sandbox has no filesystem and no system fonts: a PDF whose fonts
are neither embedded nor among the 14 standard ones may render with fallback glyphs; its
text layer is unaffected. **Interpreter fallback.** wazero compiles to native code on
amd64 and arm64 only; elsewhere it interprets, about 60 times slower, and `config check`
warns. Details: [PDF preprocessing](pdf.md).

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
| `judge.policy` | string | `roles.judge.policy` | The application's judge policy: `always`, `never` or `auto` (see [judge policy](judge-policy.md)). Hashed into the application's `service_version` unless `always`. |
| `judge.allow_override` | bool | `false` | Lets a request choose the policy with the `judge` part (or JSON field); without it the part is refused with `400 bad_request`. The application then has one `service_version` per policy, and each job carries the one it ran under. |
| `judge.sample_rate` | number | `0.05` | Under `auto`, the share of jobs (chosen deterministically from the job id) sent to the judge although grounding alone would accept them, to measure disagreement. From 0 to 1; hashed into `service_version` under `auto` only. |
| `documents.pdf.mode` | string | top-level `documents.pdf.mode` | Per-application PDF mode override: `auto`, `native`, `text` or `pages`. The other `documents.pdf` keys are process-wide. |
| `webhook.url` | string | empty (no webhook) | Completion webhook (v0.3): the service POSTs a signed `job.finished` notice (id, status, `service_version`, `finished_at`; never content) here when a job of the application is terminal. Must be `https`, unless loopback or `server.insecure_http` is true; no credentials in the URL. Redirects are not followed. Only configured applications have webhooks, never those created over the admin API. See [the API guide](../api/guide.md#completion-webhooks). |
| `webhook.secret_env` | string | required with `url` | Name of the environment variable holding the HMAC-SHA256 signing secret shared with the receiver; must be set; under 16 bytes is a warning. |
| `webhook.events` | list | `[job.finished]` | Events to send; `job.finished` is the only one. |

Applications are mirrored into the database when `vde serve` starts: an application
removed from the file is disabled, its tokens stop working, and its jobs remain until they
expire. Applications created over the [admin API](../api/admin.md) are kept as stored; for
the applications the file declares, the file wins at every start. `vde app list` shows
them. All counts above (`max_attempts`, `quota.limit`, `rate.*`) must be at least 1. See [applications and tokens](applications-and-tokens.md).

## `sync` (used by `vde serve`)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `sync.max_wait` | duration | `60s` | Longest time `POST /v1/extract` holds a request open for the job to finish before answering `202`; positive and at most `120s`. The handler extends the connection's read and write deadlines for the wait; a reverse proxy in front needs a read timeout above it. |

## `auth`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `auth.max_token_ttl` | duration | `17520h` (730 days) | Longest lifetime `vde token create --expires` accepts; every token expires. |

## `logging` and `debug`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `logging.level` | string | `info` | `debug`, `info`, `warn` or `error`. What is logged: [privacy](privacy.md). |
| `logging.format` | string | `json` | `json` or `text`. |
| `debug.log_content` | bool | `false` | Deprecated and without effect (removal in 2.0): no content is logged anywhere. Still accepted; `true` makes `vde config check` and `vde serve` warn, and is refused with a non-loopback `server.listen`. |

## `worker`, `retention`, `audit` and `metrics` (used by `vde serve`)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `worker.size` | int | `4` | Jobs run at once by the worker pool, 1 to 256. Also the most concurrent provider calls the service starts. |
| `worker.poll_interval` | duration | `500ms` | Sleep of an idle worker between looks at the queue (a submission wakes one of the same process at once); at least `10ms`. Across processes (`vde serve --role api` and worker replicas) it is the wake-up: a job waits at most this long for a worker of another process. |
| `worker.lease` | duration | `2m` | How long a worker's lease on a running job lasts without renewal; at least `3s`. The worker renews it every third of it and at each attempt; a job whose worker died is leased again by any process once it passed. 2 minutes survives two missed renewals (a database failover, a long pause) and still resumes a job well inside the 5-minute `limits.job_timeout`; shorter risks a second worker taking a live job during a database hiccup (the first then loses its lease and discards its result), longer delays recovery. |
| `rate.shared` | bool | `false` | Count every application's `rate.requests_per_minute` and `rate.submits_per_minute` in the job store, in fixed one-minute windows (UTC) shared by every replica, one `UPSERT` per request; a refused request gets `Retry-After` until the next minute. `false` keeps per-process token buckets: N replicas then admit up to N times each limit. With `storage.driver: sqlite` it is a warning (one process: the buckets are already exact). See [storage](storage.md#which-to-choose). |
| `retention.sweep_interval` | duration | `1m` | Time between expiry sweeps (expired jobs, stale running jobs, old quota, token and audit rows); at least `1s`. |
| `audit.log` | bool | `false` | Also write each audit event the service records as an info log line with `audit=true` (never content). The events are always stored in the database. See [operations](operations.md#audit-log). |
| `audit.retention` | duration | `8760h` (365 days) | How long audit events are kept; at least `24h`. |
| `metrics.token_env` | string | empty | Name of an environment variable holding a bearer token that `GET /metrics` requires; the variable must be set when named. |
| `metrics.listen` | string | empty | A second listener, `host:port`, serving only `/metrics` (with the token when `token_env` is set); `/metrics` then answers `404` on `server.listen`. It is always plain HTTP (`server.tls` does not apply to it): not loopback is a warning, without a token because anyone can read the metrics, with one because the token crosses the network unencrypted. With neither key, `/metrics` on `server.listen` answers only loopback peers that did not come through a proxy. |
| `metrics.go_runtime` | bool | `false` | Add the Go runtime and process series to `/metrics`. The series are listed in [operations](operations.md#metrics). |
| `admin.listen` | string | `127.0.0.1:8091` | The admin API's own listener (`/admin/v1`, admin tokens from `vde token create --admin` only; see [the admin API](../api/admin.md)). Empty disables it. Must differ from `server.listen` and `metrics.listen`; a non-loopback address is refused unless `server.tls` is set (the admin listener serves the same certificate) or `server.insecure_http` is true. `server.trusted_proxy` does not apply to it. |

`GET /healthz` is unauthenticated and not rate limited: it runs a one-row database query
(1 s timeout), reads the role groups' in-memory state and checks the data directory is
writable at most every 30 s. Expose it to the orchestrator, not the public internet. Its reasons are explained in
[operations](operations.md#health-healthz).

## `storage` and `blobs`

Where job rows and document bytes live. The defaults keep everything in `server.data_dir`;
several replicas need `postgres` and `s3`. When to choose which, pool sizing, backups and
the bucket lifecycle rule: [storage](storage.md). Every command that opens the store (`serve`,
`app`, `token`, `audit`, `migrate`) uses these keys.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `storage.driver` | string | `sqlite` | `sqlite` (`server.data_dir/vde.db`, one `vde serve` process) or `postgres` (PostgreSQL 13 or later, any number of replicas). Anything else is an error. |
| `storage.dsn_env` | string | `VDE_DATABASE_URL` with `postgres` | Name of the environment variable holding the PostgreSQL connection string, a `postgres://` URL or a `key=value` string. Must be set with `postgres`. The DSN carries the password: it is never in the file, never printed by `config check` and redacted from errors. Ignored (a warning) with `sqlite`. |
| `storage.max_open_conns` | int | `16` (when `0`) | PostgreSQL connection pool size, 0 to 1000; half of it is kept idle, connections are recycled after 30 minutes. Fewer than `worker.size` + 2 is a warning. Ignored with `sqlite`. |
| `blobs.driver` | string | `fs` | `fs` (files under `server.data_dir/blobs`) or `s3` (an S3-compatible bucket: AWS S3, MinIO, Cloudflare R2, Backblaze B2). `fs` with `storage.driver: postgres` is a warning, since replicas on other hosts cannot read each other's documents. |
| `blobs.bucket` | string | required with `s3` | Bucket name (3 to 63 lowercase letters, digits, dots, hyphens). Must exist: `serve` checks it at start. Objects are `jobs/<job id>/document` and `jobs/<job id>/page-NN`. |
| `blobs.region` | string | required with `s3` | The bucket's region (`eu-central-1`; `auto` for R2; any value for MinIO). It decides where documents are stored: see [privacy](privacy.md#data-residency). |
| `blobs.endpoint` | string | empty (AWS S3) | S3 API URL of a non-AWS store, `https://host[:port]` without a path. Plain `http://` on a non-loopback host is a warning. |
| `blobs.path_style` | bool | `false` | Address the bucket in the URL path instead of the host name; MinIO and most self-hosted stores need `true`. |
| `blobs.sse` | string | `AES256` | Server-side encryption header on every upload: `AES256` (SSE-S3) or `""` for none (MinIO without a KMS refuses SSE-S3). |

S3 credentials come from the AWS default chain, never from the file: `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` (plus `AWS_SESSION_TOKEN`), a profile in `~/.aws` (`AWS_PROFILE`),
or the instance, task or pod role.

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
(fully local, no key; no PDF reader, so PDFs are sent as their text layer or as rendered
pages under `documents.pdf.mode: auto`). Every example carries a commented
`documents.pdf` block and `roles.judge.policy`.

The service keys not shown in the examples (`limits`, `worker`, `retention`, `audit`,
`metrics`, `auth`, `logging`, `storage`, `blobs`) all have defaults and are only set when you need to change
them; the tables above are the complete list.

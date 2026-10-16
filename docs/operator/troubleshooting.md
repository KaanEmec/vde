# Troubleshooting

What an operator checks when something is wrong. Start with the request id: every API
response carries `X-Request-Id` (and `error.request_id`), and every log line of that request
has the same `request_id`. Callers' side of the same codes is in the
[API guide](../api/guide.md#errors).

Quick checks:

```
vde config check --config vde.yaml     # configuration valid?
curl -sS http://127.0.0.1:8090/healthz # can the service accept jobs?
vde app list; vde token list --app <id>
docker compose logs vde                # or journalctl -u vde
```

## HTTP errors

Every error is `{"error": {"code", "message", "request_id"}}`; messages are fixed text and
never quote a document.

| Status | Code | What the operator checks |
| --- | --- | --- |
| 400 | `bad_request` | Plain HTTP where HTTPS is required (message "HTTPS is required."): the caller used `http://` against a non-loopback service, or the proxy does not send `X-Forwarded-Proto: https` with `trusted_proxy: true`. See [install](install.md#tls-or-a-reverse-proxy). Other messages describe a malformed request (multipart, missing or repeated part, bad `retention` or `max_attempts`, empty or undecodable document, `Idempotency-Key` too long): the caller's fault. |
| 401 | `unauthorized` | See [401](#401-unauthorized). |
| 403 | `forbidden` | The token's scopes do not allow the route: `jobs:read` cannot submit or delete. `vde token list --app <id>` shows scopes; create a token with `jobs`. |
| 404 | `not_found` | Unknown route, or a job that does not exist for this token's application: expired (`retention.default`, or 24 hours), deleted, already read under `retention: none`, or another application's id. Also `/metrics` on `server.listen` when `metrics.listen` is set or the request came through a proxy. |
| 405 | `method_not_allowed` | Wrong HTTP method; the `Allow` header lists the right ones. |
| 413 | `payload_too_large` | A part or the whole request is over `limits.document_bytes` (25 MiB), `limits.page_bytes` (10 MiB), `limits.pages` (20) or `limits.request_bytes` (128 MiB). Raise the limit if the consumer legitimately needs it; a reverse proxy may also impose its own body limit (the response then does not come from `vde`). |
| 415 | `unsupported_media_type` | The document's media type is never accepted, or a part's content does not match its declared type. |
| 415 | `unsupported_document` | The type is accepted by the service but no configured model of the roles can read it, for example a PDF when no model lists `application/pdf` and the caller sent no page images. Fix `accepts` or the models: [providers](providers.md#declaring-accepts), and `vde config check` shows what each role reads. |
| 422 | `invalid_schema` | The caller's schema is not valid JSON Schema 2020-12. |
| 422 | `idempotency_mismatch` | The same `Idempotency-Key` was sent with a different request. |
| 429 | `rate_limited` | A short-term limit of the application: `rate.requests_per_minute`, `rate.submits_per_minute` or `rate.max_in_progress` (the message says which). `Retry-After` says when. Raise the limit in the configuration (and restart) if it is legitimate load; watch `vde_jobs_in_progress`. |
| 429 | `quota_exhausted` | The application spent `quota.limit` for the window; `X-Quota-Remaining: 0`, `X-Quota-Reset` says when it resets. Raise `quota.limit` or `quota.window` if intended. |
| 500 | `internal` | A failure inside the service, or "The service's document configuration is not usable." Look up the request id in the logs (`level` `ERROR` or a `handler panic` line with a stack). |
| 502 | `provider_unavailable` | Reserved for provider failures; in v0.2 these appear as a failed job, not as a submit response. See [job errors](#job-errors). |
| 503 | `unavailable` | "The job could not be stored; submit it again.": the database or disk failed on that request; check disk space, permissions and `/healthz`. "Job submission is not configured on this service.": the process was built without its job store; should not occur with `vde serve`. On `/healthz`, see [below](#healthz-503). |
| 504 | `timeout` | Reserved; a job that hits its time limit is a failed job with code `timeout`. |

### 401 unauthorized

All token problems answer the same `401` so a caller cannot probe. The operator finds the
reason in the audit log: `auth.failed` events carry `reason`.

```
sqlite3 -readonly /var/lib/vde/vde.db \
  "SELECT datetime(at/1000,'unixepoch'), application_id, details FROM audit_events
   WHERE kind='auth.failed' ORDER BY id DESC LIMIT 20"
```

| `reason` | Cause | Fix |
| --- | --- | --- |
| `missing` | No `Authorization: Bearer` header. | The caller must send it (a proxy may strip it). |
| `malformed` | Not shaped like `vde_<id>_<secret>`; truncated, quoted or with whitespace. | Check how the caller stores the token. |
| `unknown_token` | No token with this id in this database. | Wrong environment or data directory, or the database was restored from before the token existed. Create a new token. |
| `wrong_secret` | The id exists, the secret does not match. | A corrupted or guessed token. |
| `revoked` | `vde token revoke` was run. | Create and deploy a new token. |
| `expired` | Past its expiry. | Rotate: [applications and tokens](applications-and-tokens.md#rotate). |
| `application_disabled` | The application is no longer in the configuration. | Add it back and restart. |

## Job errors

A job that ends `failed` carries `error.code`. A `rejected` job is not an error: the judge
did not accept the last candidate within the attempts; read `feedback`. The caller-facing
meaning is in the [API guide](../api/guide.md#interpreting-the-status).

| Code | Meaning | What the operator checks |
| --- | --- | --- |
| `provider_unavailable` | Every endpoint of a role's group failed with retryable errors, or `failover.max_tries` or `max_total_time` ran out. | Provider outage, network or DNS from the host, a `429` from the provider (rate limit or spend cap on your account), a too-short endpoint `timeout`. `vde_provider_calls_total{outcome="retryable"}` and the `vde_failovers_total` series show which endpoint. Add a fallback pair to the group. |
| `extractor_failed` | The extractor gave no usable answer on any attempt: refusals or bad requests from the provider, or never valid JSON. | A permanent provider error on every attempt: wrong or revoked key (`401`), unknown model name, a model that cannot read a type its `accepts` list claims. `vde_provider_calls_total{outcome="permanent"}`. Try the document with `vde extract --verbose`. |
| `judge_failed` | The judge's verdict could not be parsed on any try (`limits.judge_reparse_tries`), or the judge's provider refused. | Same checks as above for the judge group; a weak model that does not follow the verdict format; raise `limits.judge_reparse_tries`; use a stronger judge. |
| `timeout` | The job exceeded `limits.job_timeout` (default 5 minutes, at most 8), or its worker was lost and the sweeper finished it. | Slow models or large documents; raise `limits.job_timeout` (keep `failover.max_total_time` below it); queue time counts too, so check `worker.size` and `vde_jobs_in_progress`. |
| `internal` | An unexpected failure in the job loop. | The log lines of the job (`job_id`) with `level` `ERROR`. |

## Provider failures

Seen in `vde_provider_calls_total`, in logs (the HTTP status and the provider's error code,
never the body) and as the job errors above.

| Symptom | Cause and fix |
| --- | --- |
| `401` or `403` from a provider | A wrong, expired or revoked key, or a key for another project. It is a permanent failure: it is not retried or failed over (unless `failover_on_permanent`), every attempt fails, and the job ends `extractor_failed` or `judge_failed` after the attempts. `/healthz` stays `ok` because the endpoint is not marked down. Check the variable named by `api_key_env` in the running process (`docker compose exec vde env` shows names; do not print values into shared logs), fix, restart (keys are read at start). |
| `429` from a provider | Retryable: the router honors a short `Retry-After`, then fails over to the next pair. If every pair is limited the job ends `provider_unavailable` and the pair is marked down for 30 seconds to 5 minutes. Raise your provider limits, add a fallback endpoint, lower `worker.size`, or lower the application's `rate.*`. |
| `5xx`, timeouts, connection errors | Retryable. If frequent, raise the endpoint's `timeout` (default 90 s; local models often need more), and check egress firewall and DNS. |
| `400` about the model name | The model id in `endpoints.<n>.models` is wrong or not available to your account. Permanent. |
| A model does not accept PDF | If no model of a role lists `application/pdf` in `accepts` (because it cannot read PDFs), a PDF is refused at submission as `unsupported_document` (`415`) unless the caller sends page images; add a PDF-capable pair, or have callers send pages. The reverse mistake, listing `application/pdf` for a model that cannot read it, is accepted at submission and then the provider refuses the document: jobs end `extractor_failed`. Remove the type from `accepts`. |
| Answers with prose or code fences | The extractor model does not follow the format; the loop gives feedback and retries; persistent cases end `rejected` or `extractor_failed`. Use a stronger model. |

`vde config check --probe` is not implemented yet: no provider has a probe, so it only warns
"probe not available" for every endpoint and tests nothing (probing is planned for v0.3). To test an endpoint, run `vde extract` with a small document.

## Worker not picking up jobs

Jobs stay `queued`:

1. `/healthz`: a `db` or `blobs` reason means the service cannot work; fix the disk or
   permissions of `server.data_dir`.
2. Is the process running, and is it the one that owns this data directory? Only
   `vde serve` runs workers; `vde extract`, `token` and `app` do not.
3. Are all workers busy? `worker.size` (default 4) jobs run at once; slow providers queue
   the rest. Raise `worker.size` (up to 256) if the provider allows the concurrency.
4. The application is at `rate.max_in_progress`: the worker skips an application while that
   many of its jobs are running. The cap is read at start.
5. Look for `worker: lease failed` errors in the logs (database problem).
6. After a crash a running job is picked up again once its one-minute lease expires; the
   sweeper fails jobs past their time limit with `timeout`.

`vde_jobs_in_progress` and `vde_jobs_total` show whether anything moves.

## `/healthz` 503

| Reason | Check |
| --- | --- |
| `db` | Is `vde.db` readable and the disk not full or read-only? Logs: `healthz: database check failed`. |
| `blobs` | Can the service user create files in `server.data_dir`? Check ownership (uid 65532 in the container), mount options and free space. Logs: `healthz: data directory is not writable`. The result is cached for 30 seconds. |
| `role_extractor_down`, `role_judge_down` | Every pair of that role's group failed retryably and is marked down (30 seconds to 5 minutes, per process). Check the provider's status and your network; it clears after a successful call or the cooldown. |

A `400` from `/healthz` itself means the HTTPS rule: behind `trusted_proxy`, send
`X-Forwarded-Proto: https` (see [install](install.md#behind-a-reverse-proxy)).

## `vde serve` does not start

| Message (start of the line) | Fix |
| --- | --- |
| `server.listen "..." is not a loopback address: set server.tls, or server.trusted_proxy ...` | The listener is not loopback and no TLS mode is set. [Install](install.md#tls-or-a-reverse-proxy). |
| `config: ... has N blocking problem(s)` | The lines above it name each key; see the next section. |
| `listen tcp ...: bind: address already in use` | Another process (or a second `vde serve` on the same port). |
| `job: open database: ...`, `job: database permissions: ...` | `server.data_dir` is not writable by the service user, or the path is wrong. |
| `job: migration NNNN_name: ...` | A migration failed; keep the message, restore the backup taken before the upgrade. |
| `metrics.listen: listen tcp ...` | The metrics address cannot be bound. |

## `config check` messages

`vde config check` prints every problem, each as `error: <key>: <message>` or
`warning: <key>: <message>`, and exits 1 on errors (and on warnings with `--strict`).
`vde serve` refuses to start on errors and prints warnings. Decoding problems come first
and carry the line: `config: line 12: endpoints.x.timeout: ...`. Unknown keys are errors
(`unknown key`), and a bare number where a duration is expected is rejected (write `90s`).

### Errors

| Key | Message | Fix |
| --- | --- | --- |
| `server.listen` | `... is not host:port` | Write `127.0.0.1:8090` or `:8090`. |
| `server.tls` | `cert_file and key_file must be set together` | Set both or neither. |
| `server.insecure_http` | `must not be true together with server.tls` | Remove one. |
| `debug.log_content` | `must not be true while server.listen ... is not a loopback address` | Turn it off, or listen on loopback. |
| `auth.max_token_ttl` | `must be positive` | |
| `endpoints` | `at least one endpoint is required` | |
| `endpoints.<n>.provider` | `"x" is not one of anthropic, openai_compatible, gemini, fake` | |
| `endpoints.<n>.preset` | `unknown preset "x"`; `presets apply only to openai_compatible endpoints` | The only preset is `openrouter`. |
| `endpoints.<n>.base_url` | `is required for provider "x" without a preset`; `... is not an absolute http or https URL` | Set `base_url` (OpenAI-compatible servers need it). |
| `endpoints.<n>.api_key_env` | `is required for provider "x"`; `... is not an environment variable name`; `environment variable X is not set` | Export the variable in the environment of the process that runs the command (not just your shell, for containers and services). |
| `endpoints.<n>.timeout` | `must be positive` | |
| `endpoints.<n>.models` | `at least one model is required` | |
| `endpoints.<n>.models.<m>.accepts` | `must list the media types the model reads`; `unknown media type "x"; known: ...` | See [providers](providers.md#declaring-accepts). |
| `groups` | `at least one group is required` | |
| `groups.<g>.endpoints` | `at least one endpoint is required` | |
| `groups.<g>.endpoints[i].endpoint` | `endpoint "x" is not defined` | Name an entry of `endpoints`. |
| `groups.<g>.endpoints[i].model` | `model "x" is not declared on endpoint "y"` | Declare it under `models`. |
| `groups.<g>.failover.max_tries` | `must be at least 1` | |
| `groups.<g>.failover.max_total_time` | `must be positive`; `X must be less than limits.job_timeout Y` | Lower it or raise `limits.job_timeout`. |
| `roles.<r>.group` | `is required`; `group "x" is not defined` | |
| `limits.max_attempts` | `must be at least 1` | |
| `limits.job_timeout` | `X must be positive and at most 8m0s` | |
| `limits.judge_reparse_tries` | `must not be negative` | |
| `limits.document_bytes`, `page_bytes`, `pages`, `request_bytes` | `must be positive` | |
| `documents.html` | `"x" is not one of raw_html, visible_text` | |
| `logging.level`, `logging.format` | `... is not one of ...` | `debug`, `info`, `warn`, `error`; `json`, `text`. |
| `applications[i].id` | `is required`; `duplicate application id "x"` | |
| `applications[i].max_attempts`, `quota.limit`, `rate.*` | `must be positive` | |
| `applications[i].quota.window` | an ISO 8601 duration error | Use `P1D`, `PT1H`, `P7D`; no years or months. |
| `applications[i].retention.default` | `"x" is neither none nor a positive duration` | `24h`, `none`. |
| `applications[i].documents.html` | `"x" is not one of ...` | |
| `metrics.token_env` | `... is not an environment variable name`; `environment variable X is not set` | |
| `metrics.listen` | `... is not host:port`; `must differ from server.listen` | |
| `worker.size` | `N is not between 1 and 256` | |
| `worker.poll_interval` | `must be at least 10ms` | |
| `retention.sweep_interval` | `must be at least 1s` | |
| `audit.retention` | `must be at least 24h0m0s` | |

### Warnings

Warnings do not block start (unless `--strict`); each deserves a decision.

| Key | Message | Meaning |
| --- | --- | --- |
| `server.insecure_http` | plain HTTP is accepted from any peer ... | Intended only for development and trusted private networks. |
| `roles.<r>.group` | `group "x" cannot read ... directly or via page images` | The group cannot read a type the service must accept (PDF, PNG, JPEG, HTML, plain text); submissions of it will be refused as `unsupported_document`. |
| `roles.<r>.group` | `no endpoint-model pair in group "x" reads application/pdf natively; PDFs work only when callers send page images ...` | The service never renders pages. |
| `limits.document_bytes`, `page_bytes`, `pages`, `request_bytes` | `N is below the contract minimum of M` | Callers may legitimately send more than you allow. |
| `applications[i].max_attempts` | `N is above limits.max_attempts M; each attempt costs model calls` | |
| `metrics.listen` | `... is not a loopback address and metrics.token_env is not set` | Anyone who reaches the port can read the metrics. |

At start `vde serve` also logs `no applications are configured: every /v1 request is
refused` when `applications` is empty (not a `config check` finding).

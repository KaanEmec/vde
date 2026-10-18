# Security and privacy

Status: written 2026-10-16 against the v0.2 implementation. Each control below is marked
**implemented** (the code is in the tree and tested) or **planned** (not in effect and not to
be relied on). The marks were checked against the code when this page was written; nothing
has yet been verified against a live deployment (see the [changelog](../CHANGELOG.md)). Report vulnerabilities as described in [SECURITY.md](../SECURITY.md).

This page is for operators who must approve the service and for consumers who must know
what it protects. It states the threat model, the controls with their configuration keys,
what the service deliberately does not do, and a checklist to complete before the service
is reachable from the internet. Design background is in the
[architecture overview](architecture/overview.md#security-model) and the project brief
("Security and access tokens"); the logging rules are in [operator/privacy.md](operator/privacy.md).

## Threat model

### What is protected

| Asset | Where it lives |
| --- | --- |
| Documents and page images | Provider requests in flight; the blob directory (`<data_dir>/blobs`) until deleted |
| Instructions, schemas, results, feedback | SQLite `jobs` table until cleared; provider requests in flight |
| Access tokens | Shown once to the operator; only a hash is stored |
| Provider API keys | Environment variables of the `vde` process |
| Provider spend | Quota and rate limits per application |

### Who can reach what

| Actor | Can reach | Notes |
| --- | --- | --- |
| Anyone who knows the URL | `GET /healthz` (status and reason codes only) | Every other `/v1` path needs a valid token and answers a generic `401` otherwise. Knowing the URL grants nothing. `/metrics` answers only loopback peers (or needs its own bearer token, or its own listener; see the table below). |
| Holder of a valid token | The `/v1` routes its scopes allow, for its own application's jobs | Another application's job ids answer `404`. |
| Operator with host access | Everything: configuration, provider keys in the process environment, database, blobs, the `vde token` and `vde app` commands | There is no weaker operator role. |
| Holder of an admin token | The admin API (`/admin/v1`) on `admin.listen`, a listener separate from the `/v1` API: create, change and disable applications, create and revoke tokens, list and delete jobs (metadata only, never content), read the audit log | Admin tokens have no application and the single scope `admin`; they are created only with `vde token create --admin` and are refused on `/v1`, as application tokens are refused on the admin API. 60 requests per minute per token. Every change is audited with the actor `admin:<token id>`. Default listener `127.0.0.1:8091`. |
| Anyone on the network path | Whatever is not encrypted | HTTPS is required outside loopback, so tokens and documents are encrypted in transit unless the operator turns on `server.insecure_http`. |
| Model providers | What the configured endpoints receive | See "What providers receive". |

### What a leaked token can do

- Everything its scopes allow for its one application, and nothing for another
  application: with `jobs` it can submit, poll, delete and read quota; with `jobs:read`
  only poll and read quota. It cannot create or revoke tokens (the HTTP API has no
  token routes on `/v1`), change configuration or read other applications' jobs.
- It can spend that application's quota and provider budget, up to `quota.limit` per
  `quota.window` and the `rate.*` limits. Those limits are the cap on the cost of a leak.
- It works until the token's `expires_at`. Every token has one; the default cap is 730 days
  (`auth.max_token_ttl`). Use short lifetimes and rotate.
- Revocation (`vde token revoke <id>`) takes effect on the very next request: the service
  reads the token from the database on every request and keeps no cache. Several active
  tokens per application allow rotation: create the new token, deploy it, revoke the old one.
- A leaked token does not reveal documents already deleted, and jobs of an application with
  `retention.force_none` hold no content after the first terminal read.

### What a leaked admin token can do

Everything the admin API offers, for every application: create and disable applications,
mint and revoke their tokens, delete jobs, and read job metadata (status, sizes, timestamps,
never results, instructions or documents) and the audit log. It cannot read document
content, change the configuration file, reach provider keys or submit jobs. It works only
on the admin listener, which is loopback by default, so exposing that listener to a network
is what makes a leak reachable. Admin tokens expire like all tokens, are revoked with
`vde token revoke`, and every change they make is audited.

### What a leaked database file reveals

The SQLite file (`<data_dir>/vde.db`, mode `0600`) holds:

| Data | Content |
| --- | --- |
| Tokens | Token id (the clear 8-character part), SHA-256 hash of the secret, scopes, label, timestamps. The hash of a random 256-bit secret cannot be used to authenticate and is not practically reversible; the token itself is not recoverable. |
| Applications | Id, name, limits, retention policy, mirrored from the configuration |
| Jobs, always | Id, application, status, attempts, media type, size, page count, timestamps, `service_version`, error code, idempotency key and a hash of the request body |
| Jobs, content | Extractor and judge instructions, schema, result, feedback and error text. Present for jobs whose retention has not yet cleared them: `retention: default` jobs until `expires_at` (24 hours unless configured), `none` jobs until the first terminal read or 24 hours. Results typically contain personal data (names, itineraries, booking references). |
| Quota counters, audit events | Counts per window; event kinds, application, actor, job id, fixed-shape details. Never content. |

The documents and page images are not in the database but in separate files under
`<data_dir>/blobs/` (directories `0700`, files `0600`). With `retention: none` they are
deleted when the job is terminal; with `default` at `expires_at`.

Deleting clears columns and unlinks files; it does not wipe storage. The database sets
`secure_delete`, so SQLite overwrites deleted content in the database file, and after every
statement that deletes job content (first read, expiry, `DELETE`) the store runs
`PRAGMA wal_checkpoint(TRUNCATE)`, so the write-ahead log no longer holds the earlier page
images either: once the checkpoint completes, the content is in none of `vde.db`,
`vde.db-wal` and `vde.db-shm` as files. The checkpoint is best effort: a long reader can
keep it from completing (logged at debug), and the next one catches up. What is not
guaranteed: the file system and the disk may keep the freed blocks of the truncated WAL and
of unlinked blob files, which can be recovered from the volume. Protect the volume with host
disk encryption (see the checklist); do not treat deletion as erasure against someone with
raw disk access.

### What providers receive

Only endpoints listed in the operator's configuration ever receive anything; a request can
name neither an endpoint nor a model. A provider call carries the document (bytes, or page
images, or text for text, HTML and email), the caller's extractor or judge instructions,
the schema, the previous candidate and the feedback. What the provider retains, and whether
it trains on the data, is a property of the provider, your account and the endpoint you
configure. Each adapter sets the no-training and minimal-retention options its API exposes
and documents what must be set at account level: [provider adapters](providers/README.md)
(one page per adapter). Review the page of every adapter you enable. A local endpoint
(Ollama, vLLM) keeps documents on your hosts.

## Controls

| Control | Configuration | Status | Notes |
| --- | --- | --- | --- |
| Authentication on every `/v1` route | none (always on) | implemented | Bearer token; missing, malformed, unknown, wrong-secret, revoked, expired or disabled-application tokens all answer one generic `401` with `WWW-Authenticate`. Checked before the body is read, before any provider call and before quota. Only `/healthz` is unauthenticated. Each refusal writes an `auth.failed` audit row, at most 10 per second (burst 30) per listener; refusals over that are counted in `vde_audit_auth_failed_dropped_total`, so requests without a token cannot fill the database. |
| Token format and hashing | `auth.max_token_ttl` | implemented | `vde_<8-char id>_<32 random bytes, base64url>`. The secret is stored as SHA-256 and compared in constant time; shown once by `vde token create`. A test greps the data directory for secrets. |
| Expiry, revocation, rotation | `vde token create --expires`, `vde token revoke`, `auth.max_token_ttl` | implemented | Expiry is mandatory and capped. Revocation is effective on the next request. Rotation is manual (several active tokens per application). |
| Scopes | `vde token create --scope` | implemented | `jobs` (submit, poll, delete, quota) and `jobs:read` (poll, quota). Unknown scopes are refused at creation; insufficient scope answers `403`. |
| Application disable | `applications[]` in the configuration | implemented | An application removed from the configuration is disabled at start and its tokens stop working. |
| Ownership of jobs | none | implemented | Job routes answer `404` for another application's job ids, the same answer as for an unknown, expired or deleted job. |
| HTTPS required | `server.tls.cert_file`, `server.tls.key_file`, `server.trusted_proxy`, `server.insecure_http` | implemented | Plain HTTP from a non-loopback peer answers `400 bad_request`. `vde serve` refuses a non-loopback plain-HTTP listener unless one of the three is set. TLS 1.2 or newer. See the checklist for the proxy caveat. |
| Provider credentials | `endpoints.<name>.api_key_env` | implemented | Keys are read from the named environment variable; they never appear in the file, `config check` output, logs or responses. |
| Endpoint and model allow-listing | `endpoints`, `groups`, `roles` | implemented | The request has no field that selects an endpoint or model. Documents go only to the configured groups. |
| Upload limits | `limits.document_bytes`, `limits.page_bytes`, `limits.pages`, `limits.request_bytes`, `limits.max_submits_in_flight` | implemented | Enforced by the submit handler: `413` over a size limit. Defaults 25 MiB, 10 MiB, 20 pages (at most 999), 128 MiB per request. At most `limits.max_submits_in_flight` (default 16) submissions are read and prepared at once per process, and at most half of them (at least one) per application; one more answers `503 unavailable` with `Retry-After` before any part is read. Server read timeouts (60 s for the whole request) and a 64 KiB header cap also apply. |
| Quota | `applications[].quota.limit`, `quota.window` | implemented | Submissions per window, default 1000 per `P1D`; `429 quota_exhausted`. Counters are stored in the database, so they survive a restart. |
| Rate limits | `applications[].rate.requests_per_minute`, `submits_per_minute`, `max_in_progress` | implemented | Defaults 300, 30, 20; `429 rate_limited` with `Retry-After`. In memory per process, so each replica applies its own. The admin API has its own fixed limit of 60 requests per minute per admin token. |
| Retention | `applications[].retention.default`, `retention.force_none`; per request `retention` | implemented | `none` deletes the document and pages at the terminal state and the result on the first terminal read or after 24 hours; `default` follows the operator's duration; `force_none` overrides requests. Deletion points are the worker at the terminal state, the read and `DELETE` handlers, and the expiry sweeper (every `retention.sweep_interval`), which also finishes stale jobs and removes old quota, token and audit rows. See [operator/retention.md](operator/retention.md). |
| Redaction of logs, metrics, errors | `logging.level`, `logging.format` | implemented | The logger drops every attribute not on a fixed allow-list and cuts long values; the extraction loop logs through the logger its caller passes (the worker's, `vde extract`'s, `vde eval`'s), and `vde serve` also makes it the process default, so no package-level `slog` call bypasses it; error messages are fixed templates enforced by a lint test; a test plants markers in content and fails if they reach a log line. Metrics labels are configuration ids and fixed vocabularies only (application, endpoint, model, route pattern, status, outcome, role), never request data. |
| Content debug switch | `debug.log_content` (default `false`) | validation implemented; content sink planned | `vde config check` and `vde serve` refuse it unless `server.listen` is loopback. With it on, `vde serve` opens a separate `0600` file (`<data_dir>/content.log`) and warns, but no code writes content to it yet. Content never goes to the routine log. Unsafe: leave off. |
| Audit events | `audit.log`, `audit.retention` (default 365 days) | implemented | Stored in the `audit_events` table: `config.loaded`, `token.created`, `token.revoked`, `app.enabled`, `app.disabled`, `auth.failed` (reason code, never the token), `job.created`, `job.finished`, `job.deleted`, `job.expired`, `webhook.delivered`, `webhook.failed`. `audit.log: true` also writes each service-recorded event as a log line. Read them with `vde audit list` or `GET /admin/v1/audit`. Events never contain content. |
| Admin API | `admin.listen` (default `127.0.0.1:8091`; empty disables) | implemented | Separate listener, admin tokens only (`vde token create --admin`), never accepted on `/v1`. 60 requests per minute per admin token. Changes are audited under `admin:<token id>`; job listings carry metadata only. A non-loopback `admin.listen` is refused unless `server.tls` or `server.insecure_http` is set. `server.trusted_proxy` does not apply to it: do not put it behind the public proxy. |
| `/healthz` | none | implemented | Unauthenticated; returns `ok` or `503` with reason codes (`role_extractor_down`, `role_judge_down`), nothing about documents or configuration. On `--role api` it also returns `queue_age_seconds`, the age of the oldest queued job across all applications. Its database query runs one at a time, at most 10 in a burst and 5 times per second for the process, under its own 1 s timeout (a client hanging up does not fail it); requests over that rate or during a running query get the last result, never a `429` (see "Hardening review (v1.0)"). |
| `/metrics` | `metrics.token_env`, `metrics.listen`, `metrics.go_runtime` | implemented | With `token_env` it needs that bearer token (compared in constant time; `vde serve` refuses an unset variable). With `listen` it moves to its own listener and answers `404` on `server.listen`. With neither, only loopback peers without a proxy forwarding header are served. `metrics.listen` is always plain HTTP: a non-loopback address is a warning, without a token (anyone can read the metrics) and with one (the token crosses the network unencrypted). Do not expose it publicly. |
| Secure delete | none (always on) | implemented | The database sets SQLite `secure_delete`, so deleted content is overwritten in the file, and checkpoints the write-ahead log (`wal_checkpoint(TRUNCATE)`, best effort) after each content deletion, so the content is not left in `vde.db-wal` either. Not guaranteed: blocks the OS freed (the truncated WAL, unlinked blob files) are not wiped; use host disk encryption (see "What a leaked database file reveals"). |
| Document URL fetch (SSRF) | `documents.fetch.*` (default off) | implemented (v0.3) | JSON submissions may name a document `url` only when enabled: `https` only, exact host allow-list, redirects only to the same host (at most 3), every connection's resolved address checked at dial time (loopback, private, link-local, unspecified, multicast, shared, NAT64 refused, so DNS rebinding gains nothing), no environment proxy, size cap at `limits.document_bytes`, whole-fetch timeout. Residual risk: an allowed host that serves attacker-chosen content or open redirects within itself. |
| Completion webhooks | `applications[].webhook` | implemented (v0.3) | URL set by the operator, never by a request; `https` unless loopback or `server.insecure_http`; redirects not followed. Signed `X-VDE-Signature: t=<unix>,v1=<HMAC-SHA256 of "<t>.<body>">` with a secret from the environment; the payload is metadata only (no result, feedback or error). Receivers must check the timestamp window to resist replay. Attempts audited as `webhook.delivered` / `webhook.failed`. |
| PDF parsing sandbox (pdfium) | `documents.pdf.mode`, `dpi`, `max_pages`, `workers` | implemented (v0.4) | PDFs the service prepares (text layer, rendered pages, grounding text; [ADR 0010](architecture/decisions/0010-pdf-preprocessing-in-service.md)) are parsed by pdfium compiled to WebAssembly and run by wazero in process, not by native code. The sandbox mounts no host directory (no filesystem), has no network (WebAssembly has no sockets and no such host functions are given), discards pdfium's stdout and stderr, and caps its memory at 1 GiB per instance. Each document gets a fresh instance, released after it; a call that times out (30 s), fails or panics kills its instance. Page count (`max_pages`), document size (`limits.document_bytes`), dpi (36 to 600) and page bitmap size (40 megapixels) are bounded; `workers` bounds instances per process (budget about 500 MB each). `FuzzOpen` fuzzes the parser entry. The text layer is never logged. Residual risk: a pdfium bug can corrupt only its own linear memory, or exhaust its CPU until the timeout. |
| Response headers | none | implemented | `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Request-Id` on every response. |
| File permissions | `server.data_dir` | implemented | Database `0600`, blob directories `0700`, files `0600`; the data directory is created `0700` when missing. The container runs as the unprivileged user 65532 on a distroless image. |

## What the service does not do

These are deliberate limits, not gaps awaiting a fix in v0.2.

- **No encryption at rest.** The service does not encrypt the database or blobs. Use an
  encrypted volume or disk on the host (contract §10 allows this).
- **No tamper-evident audit.** Audit rows are append-only by convention, in the same SQLite
  file as everything else; there are no hash chains or external signing. Anyone with write
  access to the database can alter them. Ship the log stream elsewhere if you need that.
- **One trust boundary: the process.** API, worker and sweeper share one process, one
  database and one set of provider keys. A flaw that gives code execution exposes all of
  it. HTML and email are parsed in process, in memory-safe Go, with fuzz tests; PDFs that the
  service prepares itself are parsed by pdfium inside a WebAssembly sandbox (see the
  control above), and PDFs sent as given go to the provider unparsed.
- **No secure erase.** See "What a leaked database file reveals".
- **No defense against prompt injection.** A document can contain text aimed at the
  models. The judge checks the candidate against the source but is a model, not a security
  boundary. Treat results as untrusted input and validate them in the consuming application.
- **No per-user identity.** Tokens identify an application, not a person. Browser and
  mobile clients must not hold a token; call the service from your backend.
- **No IP allow-listing, mTLS, WAF or per-IP rate limiting.** Do these at the reverse
  proxy or network if you need them. Rate limits are per application, per process.
- **No masking of sensitive data inside documents.** The models see the document as sent.
- **No malware scanning** of uploaded documents.
- **No automated token rotation or key management.** Rotation is a manual CLI procedure;
  provider keys are read at start, so changing one needs a restart.
- **No provider-side guarantees.** Retention and training are the provider's behavior; the
  service sets what each API exposes and documents the rest.

## Operator checklist before exposing the service to the internet

Complete every item; the service does not check them for you.

1. **TLS.** Set `server.tls.cert_file` and `key_file`, or put a reverse proxy in front and
   set `server.trusted_proxy: true`. Never set `server.insecure_http` on a reachable listener.
2. **If you use `trusted_proxy`:** the service believes `X-Forwarded-Proto: https` from
   any peer and does not check the peer address. Make the service reachable only through
   the proxy (bind to loopback or a private network, firewall the port), and have the
   proxy overwrite the header rather than pass the client's.
3. **Proxy logs.** Configure the proxy not to log the `Authorization` header, request
   bodies or query strings.
4. **Encrypt the volume** that holds `server.data_dir` (database and blobs), and restrict
   it to the service user. Back up the database only to equally protected storage.
5. **Provider settings.** Read the page of each enabled adapter under
   [providers/](providers/README.md), set the account-level data-handling options it names
   (no training, minimal retention), and remove endpoints you have not approved.
6. **Tokens.** One application per consuming environment; one secret store entry per
   token; short expiries (`--expires`) and a lower `auth.max_token_ttl` than the default;
   the minimum scope (`jobs:read` for poll-only clients); a rotation routine.
7. **Limits.** Set each application's `quota`, `rate` and the document limits to what the
   consumer needs, since they bound the cost of a leaked token.
8. **Retention.** Choose `retention.default` per application, and set
   `retention.force_none: true` where the consumer must keep nothing.
9. **Logging.** Leave `debug.log_content` off. Keep `logging.level` at `info` or higher in
   production, and treat logs as operational data that still contains job ids and application ids.
10. **Admin access.** Restrict the host and the `vde token`, `vde app` and `vde audit`
    commands to operators; they act directly on the database. Keep `admin.listen` on
    loopback or a private network, or empty it if you do not use the admin API; use short-lived
    admin tokens and never hand them to a consuming application.
11. **Metrics.** Do not publish `/metrics`; set `metrics.token_env` (or `metrics.listen` on a
    private interface) and scrape it from the private network.
12. **Verify.** Run `vde config check`; from outside, confirm a request without a token
    answers `401`, plain HTTP answers `400` or is refused, and `GET /healthz` reveals nothing beyond its status.
13. **Process.** Run as an unprivileged user (the image does), keep the binary and base
    image up to date, and subscribe to [security advisories](https://github.com/kaanemec/vde/security/advisories).

## Hardening review (v1.0)

Internal review of 2026-10-18 against the code at that date (job
[v1.0 02-01](roadmap/v1.0-stable/02-security-review/01-hardening-pass.md)), area by area,
with the fix or the reason no change is needed. Severity: High (an unauthenticated or
token holder can exhaust or crash the service), Medium (resource exhaustion or a crash
needing a token and a crafted input), Low (defense in depth), None (checked, no change).
Every measurement is from a local run on an Apple M-series laptop.

| Area | Severity | Finding | Fix or verdict |
| --- | --- | --- | --- |
| JSON Schema: nesting | High | `CompileSchema` has no context and its cost grows faster than linearly with depth: a schema nested 9000 levels (within the 256 KiB part limit) took 152 s of CPU in the submit handler. | Fixed: schemas nested deeper than 64 levels are `422 invalid_schema` before compiling (`internal/extract/schema_limits.go` `maxSchemaDepth`, `schema.go` `CompileSchema`). The largest corpus schema is 9 deep. The worst schema within the bounds compiles in about 1.3 s. |
| JSON Schema: shared references | High | Validation has no memoization: n definitions that each apply the next one twice (`allOf` of two `$ref`s) cost 2^n evaluations. 22 levels (1.4 KB) took 2 s per validation; 40 would pin a worker forever. | Fixed: the compiled schema is walked with every reference expanded; more than 10,000 evaluations is `422 invalid_schema` (`maxSchemaExpansion`). A recursive schema (a reference cycle) is counted again per candidate, following only the members and items the candidate has, with at most 10,000 evaluations at any one location; a candidate over the bound gets schema feedback instead of validation. (Unrolling the schema to the candidate's depth instead refused valid candidates of a schema with two recursive properties from depth 11 on, found in review.) The TravelInbox schema expands to 252. |
| JSON Schema: number literals | Medium | The validator compares numbers as exact rationals: `{"multipleOf": 1e-9999999}` panicked the library at compile time (a `500`), a candidate `1e9999999` panicked validation (the job failed `internal`), and `1e999999` cost 10 to 60 ms per comparison. | Fixed: number literals with more than 1000 digits or an exponent beyond ±1000 make a schema invalid and a candidate get schema feedback; `CompileSchema` and `Validate` also recover a library panic as an invalid schema or feedback. |
| JSON Schema: regular expressions | None | `pattern` and `patternProperties` use Go's `regexp` (RE2): matching is linear in the input, and repeat counts over 1000 are refused when the schema compiles. | No change. |
| Image decode | Medium | Image conversion decodes at submit and in the worker. The pixel cap (120 MP) is checked from the header before decoding (`internal/document/convert.go`, `inspectImage`), but one decode at the cap holds about 1.2 GB (source, RGBA copy, rotated copy) and the number of decodes in flight followed the number of requests. | Fixed: at most 2 decodes run at once per process (`maxConcurrentDecodes`); a conversion waits for a slot. Only JPEG, PNG and WebP decoders are registered. |
| Multipart parsing | None | Body capped at `limits.request_bytes` before parsing (`http.MaxBytesReader`), each part read through its own limit, page count capped, unknown, repeated and non-`form-data` parts refused, required media types checked. A nested `multipart/*` body is only bytes of its part, and no document media type accepts it. Part headers are never reflected: errors name only the fixed part names, the filename is informational, and the media type is replaced by its canonical form. | No change. `FuzzReadSubmission` covers the reader. |
| SSRF (document URL fetch) | Low | `https` only, exact host allow-list, same-host redirects (at most 3), no environment proxy, size cap on the decoded body, whole-fetch timeout, and every dialled address checked after resolution (so DNS rebinding gains nothing). Missing from the non-public ranges: `0.0.0.0/8` (some stacks route it to the host), `240.0.0.0/4`, and the IPv6 forms that embed an IPv4 address (`::/96`, 6to4 `2002::/16`, Teredo `2001::/32`). | Fixed: the ranges are refused (`internal/api/fetch.go` `nonPublic`). Webhook URLs come from the configuration only. |
| Token comparison | None | The 8-character token id (not secret) selects the row; the secret's SHA-256 is compared with `subtle.ConstantTimeCompare` (`internal/auth/token.go` `Verify`). An unknown id skips one hash, a difference of microseconds, and every refusal is the same `401`: timing can at most confirm that an id exists, which grants nothing. The metrics token comparison is constant time except for its length. | No change. |
| SQL | None | Every statement in `internal/job` is a constant with placeholders; the only concatenations are constant column lists, the lease statement's constant fragments and an integer lock id. `rebind` rewrites placeholders only. | No change. |
| Blob names | Low | Job ids match `^[A-Za-z0-9_-]{3,64}$` and names an allow-list in both stores, so no path or key can leave the job's directory. The allow-list (`page-NN`) refused page 100 and above, which `limits.pages` allows: such a submission failed with `503` after its quota was charged. | Fixed: page names have two or three digits (`internal/blob/blob.go`). |
| HTTP limits | None | `ReadHeaderTimeout` 10 s, `ReadTimeout` 60 s, `WriteTimeout` 90 s (the sync endpoint moves its own deadline), `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB, TLS 1.2 or newer, on the API, admin and metrics listeners (`cmd/vde/serve.go` `newHTTPServer`). The number of connections is not limited. | No change; limit connections at the proxy. |
| Concurrent uploads | Low | A token holder can have many large submissions in flight at once; each is read into memory (up to `limits.request_bytes`, default 128 MiB) before the job limits apply. Bounded by `rate.requests_per_minute` and the 60 s read timeout. | Accepted at first; fixed after the independent review (F3 below): `limits.max_submits_in_flight`. |
| Webhook signing | None | HMAC-SHA256 over `"<t>.<body>"` with a secret from the environment, `hmac.Equal` and a timestamp window in `Verify`, redirects not followed, 10 s per attempt, response drained up to 64 KiB, metadata-only payload. | No change. A secret shorter than 16 bytes is a configuration warning. |
| `/healthz` | Low | Unauthenticated, and every request ran a database query. | Fixed: a global token bucket on the database check (5 per second, burst 10, no per-IP state); over the rate the last result is reused (`internal/api/health.go` `dbCheck`). A `429` or `503` was rejected because it would let anyone make orchestrator probes fail and restart healthy replicas. Volumetric floods still belong to the network: keep `/healthz` reachable only from the orchestrator and the load balancer where you can. |
| Go toolchain | High (release builds) | `govulncheck ./...` with go1.27.1 reports 9 reachable standard-library vulnerabilities (HTTP/2 server crash GO-2026-6617 and memory exhaustion GO-2026-6603, multipart header limit bypass GO-2026-6608, among others), fixed in go1.27.2 and go1.26.9. The release workflow builds with the `go.mod` version, 1.26.0. | Open: build releases with go1.26.9 / go1.27.2 or newer; rebuild the image (its `golang:1.27` base resolves at build time). |

### Dependency audit

`govulncheck ./...` (2026-10-18): no vulnerability in a third-party module; the standard
library findings are those of the toolchain row above. Licences of the direct
dependencies, all permissive: Apache-2.0 for `aws-sdk-go-v2` (with `config`,
`bedrockruntime`, `s3`), `smithy-go`, `oklog/ulid`, `prometheus/client_golang`,
`santhosh-tekuri/jsonschema` and `tetratelabs/wazero`; MIT for `jackc/pgx` and
`klippa-app/go-pdfium`; BSD-3-Clause for `golang.org/x/image`, `x/net`, `x/oauth2`,
`x/text` and `modernc.org/sqlite`; MIT and Apache-2.0 for `gopkg.in/yaml.v3`.

Image (`Dockerfile`): `gcr.io/distroless/static:nonroot` with no shell or package manager,
user 65532, one static binary built with `CGO_ENABLED=0 -trimpath`, the licences in
`/usr/share/doc/vde/`. Both base images are referenced by tag (`golang:1.27`,
`distroless/static:nonroot`), not by digest, so a rebuild picks up their fixes; rebuild
on every Go security release.

### Fuzzing

| Fuzzer | Package | Target |
| --- | --- | --- |
| `FuzzReadSubmission` | `internal/api` | The multipart reader of `POST /v1/jobs`: refusals are `400` or `413`, accepted parts are within their limits |
| `FuzzParseJSON` | `internal/extract` | The answer parser |
| `FuzzParseVerdict` | `internal/extract` | The verdict parser: pointers, message cap, confidence range |
| `FuzzNew` | `internal/document` | Document validation, email parsing, both HTML policies |
| `FuzzHTMLText` | `internal/document` | HTML to text: `raw_html` never grows, `visible_text` stays within its cap and valid UTF-8 |
| `FuzzNormalize` | `internal/ground` | Grounding normalization |
| `FuzzOpen` | `internal/pdf` | The pdfium entry |

Run one with `go test -run='^$' -fuzz='^FuzzReadSubmission$' -fuzztime=30s ./internal/api`
(Go runs one fuzz target per invocation). The CI `fuzz` job runs each for 20 s; a failing
input is uploaded as an artifact and belongs in the package's `testdata/fuzz/` as a
regression seed. Each ran clean locally for 30 s (the three new ones) or 20 s.

## Independent review (v1.0)

A second review of 2026-10-18 (job
[v1.0 02-02](roadmap/v1.0-stable/02-security-review/02-external-review.md)), read-only and
separate from the hardening pass, of authentication, authorization, data handling and the
provider boundary. It found no way across an application or scope boundary: every `/v1` job
read, delete and replay is scoped to the application in SQL, admin and application tokens
are kept apart, and no request field selects an endpoint. Severity as above; Info is
defense in depth with no present impact.

| ID | Severity | Area | Finding | Resolution |
| --- | --- | --- | --- | --- |
| F1 | Medium | Authentication | Every refused request, a missing token included, wrote one `auth.failed` row before any rate limit: an anonymous flood cost one database write each and grew the audit table for `audit.retention`. | Fixed: at most 10 rows per second, burst 30, per listener; the rest counted in `vde_audit_auth_failed_dropped_total`; the `401` is unchanged (`internal/api/auth.go`, `ratelimit.go`, `internal/metrics/metrics.go`). |
| F2 | Medium | Logging | The extraction loop and the WAL checkpoint logged through the package-level `slog` default, which nothing set to the redacting logger, so in production they bypassed the allow-list and truncation. The redaction test installed the default itself and hid it. | Fixed: `extract.Input.Logger`, set by the worker, `vde extract` and `vde eval`; `vde serve` sets the default too (`internal/extract/extract.go`, `internal/worker/pool.go`, `cmd/vde/serve.go`, `extract.go`, `eval.go`). The redaction test now captures the default separately and fails on any line there; an end-to-end test runs the built binary and checks every line is the configured JSON. |
| F3 | Medium | Input bounds | No process-wide bound on submissions in flight: each holds up to `limits.request_bytes` (about 2.75 times that on the JSON path), and the per-application burst allowed 300 at once. | Fixed: `limits.max_submits_in_flight` (default 16), at most half of it per application so one application's slow uploads cannot hold every slot, `503 unavailable` with `Retry-After` over it, before any part is read (`internal/api/submit.go`, `internal/config`). The S3 store no longer copies bytes the API already holds (`internal/blob/s3.go`). |
| F4 | Low | Quota | The document URL fetch, schema compilation and PDF opening ran before the submit rate, in-progress ceiling and quota checks. | Fixed: after the part rules and an idempotent replay, the submit rate, the in-progress ceiling and a read-only look at the quota run first, then the fetch, document checks, schema and representation, then the quota charge (`internal/api/submit.go`, `submit_json.go`). The body is still read in full first: multipart parts arrive in the caller's order and the idempotency hash covers them all. A replay of a used key with a document URL fetches it again to compare the hash, bounded by `requests_per_minute`. |
| F5 | Low | Retention | An idempotent replay of a finished `retention: none` job returned its result without the first-read deletion, as often as it was replayed. | Fixed: such a replay answers the job without its content (`result`, `feedback` and `error` empty, no `content_deleted_at`) and deletes nothing, so the first `GET` still delivers and deletes it (contract §10, and §13's resubmission after a poll timeout); once read, replays answer the content-deleted form (`internal/api/submit.go`, `jobjson.go`). A first fix made the replay itself the first read, which lost the result on that §13 path, and was replaced after review. |
| F6 | Low | Transport | `metrics.listen` is always plain HTTP, so a bearer token on a non-loopback address crosses the network in clear. | Fixed: a configuration warning (`internal/config/service.go`). |
| F7 | Low | Provider boundary | A plain `http://` endpoint `base_url` on any host was accepted silently; documents and the API key would cross the network unencrypted. | Fixed: a configuration warning on non-loopback hosts, the URL printed without credentials (`internal/config/validate.go`). |
| F8 | Info | Configuration output | The webhook URL validation error quoted a URL with credentials whole. | Fixed: printed with `url.Redacted`, an unparseable URL not at all (`internal/config/delivery.go`). |
| F9 | Info | Authentication | A valid admin token on `/v1`, or an application token on the admin listener, gets `403`, not `401`. | Accepted: only the holder of the secret can see it, and they learn nothing the right listener would not tell them. |
| F10 | Info | Transport | With `server.trusted_proxy`, any peer's `X-Forwarded-Proto` is believed, first element taken. | Accepted: documented in checklist item 2; with a trusted proxy the listener must be reachable only through it. |
| F11 | Info | Disclosure | On `--role api`, unauthenticated `/healthz` returns `queue_age_seconds`. | Accepted and documented in the controls table: one number across all applications, no ids or content. |
| F12 | Info | Debug switch | `debug.log_content` is checked against a loopback `server.listen` only (loopback with `trusted_proxy` is public), and the content file follows symlinks and keeps an existing file's mode. | Accepted while no code writes to the file; refusing it with `trusted_proxy`, `O_NOFOLLOW` and a `Chmod` come with the sink. |
| F13 | Info | Concurrency | The worker's lease query reads `max_in_progress` from the YAML applications only, missing admin-created ones and changed limits. | Accepted: submit refuses a job once queued plus running reach the stored row's limit, so running jobs never exceed it either; the lease-side check is a second guard. |

Also fixed with the review: `limits.pages` is at most 999, since stored page names have three
digits (`internal/config/validate.go`).

### Scope for an external review

An external review should cover the same four areas: authentication (token format, hashing,
expiry and revocation, the admin and application split), authorization (scopes and job
ownership in SQL), data handling (retention and deletion points, secure delete, logs,
metrics and errors) and the provider boundary (endpoint allow-listing, the document URL
fetch, webhook delivery, the pdfium sandbox). Start with `internal/api` (`auth.go`,
`submit.go`, `submit_json.go`, `fetch.go`, `jobs_get.go`, `admin.go`, `middleware.go`),
`internal/auth`, `internal/job/store.go`, `internal/logging`, `internal/router`,
`internal/provider/httpclient.go`, `internal/webhook`, `internal/pdf` and `cmd/vde/serve.go`,
with this page, the hardening review and the independent review above as input. The
provider adapters' request bodies, the Bedrock and Vertex credential chains, Postgres and S3
specifics, and the container image were outside both internal reviews.

## Not yet reviewed

This page has been written by the project's developers. The internal hardening review and
the independent review by a second person are above; the external security review has not
happened.

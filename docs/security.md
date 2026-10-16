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
| Authentication on every `/v1` route | none (always on) | implemented | Bearer token; missing, malformed, unknown, wrong-secret, revoked, expired or disabled-application tokens all answer one generic `401` with `WWW-Authenticate`. Checked before the body is read, before any provider call and before quota. Only `/healthz` is unauthenticated. |
| Token format and hashing | `auth.max_token_ttl` | implemented | `vde_<8-char id>_<32 random bytes, base64url>`. The secret is stored as SHA-256 and compared in constant time; shown once by `vde token create`. A test greps the data directory for secrets. |
| Expiry, revocation, rotation | `vde token create --expires`, `vde token revoke`, `auth.max_token_ttl` | implemented | Expiry is mandatory and capped. Revocation is effective on the next request. Rotation is manual (several active tokens per application). |
| Scopes | `vde token create --scope` | implemented | `jobs` (submit, poll, delete, quota) and `jobs:read` (poll, quota). Unknown scopes are refused at creation; insufficient scope answers `403`. |
| Application disable | `applications[]` in the configuration | implemented | An application removed from the configuration is disabled at start and its tokens stop working. |
| Ownership of jobs | none | implemented | Job routes answer `404` for another application's job ids, the same answer as for an unknown, expired or deleted job. |
| HTTPS required | `server.tls.cert_file`, `server.tls.key_file`, `server.trusted_proxy`, `server.insecure_http` | implemented | Plain HTTP from a non-loopback peer answers `400 bad_request`. `vde serve` refuses a non-loopback plain-HTTP listener unless one of the three is set. TLS 1.2 or newer. See the checklist for the proxy caveat. |
| Provider credentials | `endpoints.<name>.api_key_env` | implemented | Keys are read from the named environment variable; they never appear in the file, `config check` output, logs or responses. |
| Endpoint and model allow-listing | `endpoints`, `groups`, `roles` | implemented | The request has no field that selects an endpoint or model. Documents go only to the configured groups. |
| Upload limits | `limits.document_bytes`, `limits.page_bytes`, `limits.pages` | implemented | Enforced by the submit handler: `413` over a size limit. Defaults 25 MiB, 10 MiB, 20 pages. Server read timeouts (60 s for the whole request) and a 64 KiB header cap also apply. |
| Quota | `applications[].quota.limit`, `quota.window` | implemented | Submissions per window, default 1000 per `P1D`; `429 quota_exhausted`. Counters are stored in the database, so they survive a restart. |
| Rate limits | `applications[].rate.requests_per_minute`, `submits_per_minute`, `max_in_progress` | implemented | Defaults 300, 30, 20; `429 rate_limited` with `Retry-After`. In memory per process, so each replica applies its own. The admin API has its own fixed limit of 60 requests per minute per admin token. |
| Retention | `applications[].retention.default`, `retention.force_none`; per request `retention` | implemented | `none` deletes the document and pages at the terminal state and the result on the first terminal read or after 24 hours; `default` follows the operator's duration; `force_none` overrides requests. Deletion points are the worker at the terminal state, the read and `DELETE` handlers, and the expiry sweeper (every `retention.sweep_interval`), which also finishes stale jobs and removes old quota, token and audit rows. See [operator/retention.md](operator/retention.md). |
| Redaction of logs, metrics, errors | `logging.level`, `logging.format` | implemented | The logger drops every attribute not on a fixed allow-list and cuts long values; error messages are fixed templates enforced by a lint test; a test plants markers in content and fails if they reach a log line. Metrics labels are configuration ids and fixed vocabularies only (application, endpoint, model, route pattern, status, outcome, role), never request data. |
| Content debug switch | `debug.log_content` (default `false`) | validation implemented; content sink planned | `vde config check` and `vde serve` refuse it unless `server.listen` is loopback. With it on, `vde serve` opens a separate `0600` file (`<data_dir>/content.log`) and warns, but no code writes content to it yet. Content never goes to the routine log. Unsafe: leave off. |
| Audit events | `audit.log`, `audit.retention` (default 365 days) | implemented | Stored in the `audit_events` table: `config.loaded`, `token.created`, `token.revoked`, `app.enabled`, `app.disabled`, `auth.failed` (reason code, never the token), `job.created`, `job.finished`, `job.deleted`, `job.expired`, `webhook.delivered`, `webhook.failed`. `audit.log: true` also writes each service-recorded event as a log line. Read them with `vde audit list` or `GET /admin/v1/audit`. Events never contain content. |
| Admin API | `admin.listen` (default `127.0.0.1:8091`; empty disables) | implemented | Separate listener, admin tokens only (`vde token create --admin`), never accepted on `/v1`. 60 requests per minute per admin token. Changes are audited under `admin:<token id>`; job listings carry metadata only. A non-loopback `admin.listen` is refused unless `server.tls` or `server.insecure_http` is set. `server.trusted_proxy` does not apply to it: do not put it behind the public proxy. |
| `/healthz` | none | implemented | Unauthenticated; returns `ok` or `503` with reason codes (`role_extractor_down`, `role_judge_down`), nothing about documents or configuration. |
| `/metrics` | `metrics.token_env`, `metrics.listen`, `metrics.go_runtime` | implemented | With `token_env` it needs that bearer token (compared in constant time; `vde serve` refuses an unset variable). With `listen` it moves to its own listener and answers `404` on `server.listen`. With neither, only loopback peers without a proxy forwarding header are served. A non-loopback `metrics.listen` without a token is a warning. Do not expose it publicly. |
| Secure delete | none (always on) | implemented | The database sets SQLite `secure_delete`, so deleted content is overwritten in the file, and checkpoints the write-ahead log (`wal_checkpoint(TRUNCATE)`, best effort) after each content deletion, so the content is not left in `vde.db-wal` either. Not guaranteed: blocks the OS freed (the truncated WAL, unlinked blob files) are not wiped; use host disk encryption (see "What a leaked database file reveals"). |
| Document URL fetch (SSRF) | `documents.fetch.*` (default off) | implemented (v0.3) | JSON submissions may name a document `url` only when enabled: `https` only, exact host allow-list, redirects only to the same host (at most 3), every connection's resolved address checked at dial time (loopback, private, link-local, unspecified, multicast, shared, NAT64 refused, so DNS rebinding gains nothing), no environment proxy, size cap at `limits.document_bytes`, whole-fetch timeout. Residual risk: an allowed host that serves attacker-chosen content or open redirects within itself. |
| Completion webhooks | `applications[].webhook` | implemented (v0.3) | URL set by the operator, never by a request; `https` unless loopback or `server.insecure_http`; redirects not followed. Signed `X-VDE-Signature: t=<unix>,v1=<HMAC-SHA256 of "<t>.<body>">` with a secret from the environment; the payload is metadata only (no result, feedback or error). Receivers must check the timestamp window to resist replay. Attempts audited as `webhook.delivered` / `webhook.failed`. |
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
  it. There is no sandbox around document parsing (PDF bytes go to the provider; HTML and
  email are parsed in process, in memory-safe Go, with fuzz tests).
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

## Not yet reviewed

This page has been written by the project's developers. The review by a second person
and the external security review (v1.0) have not happened.

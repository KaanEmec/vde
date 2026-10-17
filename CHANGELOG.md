# Changelog

All notable changes to this project are recorded here, in the format of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project follows
[Semantic Versioning](https://semver.org/). Release notes on GitHub link to this file.

## [Unreleased]

### Added

- Judge policy in the extraction loop (ADR 0009). `never` ends a schema-valid candidate
  `extracted` without a judge, with grounding entries as advisory feedback. `auto` accepts a
  candidate without a judge (`verification: grounded`) when the document has text, the
  candidate answers no schema or judge feedback and every critical value is in the
  document; a deterministic sample (`judge.sample_rate`, FNV-1a of the job id, of the
  document bytes in the CLI) is judged anyway. Metrics `vde_judge_decisions_total{policy,
  decision}` and `vde_grounding_disagreements_total`; `job.finished` audit details gain
  `verification` and `judge_policy`.
- PDF preparation in the service (ADR 0010): submit, the workers, `vde extract` and
  `vde eval` select a PDF's text layer or rendered pages with one pdfium engine per
  process when no group reads PDFs natively. A PDF over `documents.pdf.max_pages` answers
  `415` naming the limit. The container image carries `LICENSE` and `NOTICE`.

### Changed

- Under every judge policy but `never`, a schema-valid candidate whose critical values are
  not in the document goes back to the extractor once, before any judge call (one extractor
  call, no judge call, never on the last attempt). Under `always` the grounding entries
  reach only the extractor, never the job's `feedback`, so TravelInbox sees contract v1
  sources only.
- Every `service_version` changes once in v0.4: the grounding rules (`ground-1`) are hashed,
  and the PDF policy (mode, dpi, minimum text characters) where it can change what is read.

## [0.3.0] - 2026-10-17

Hardening and extensions: alternative storage, more providers, other delivery shapes, an
evaluation harness and more than one replica. `/v1` semantics are unchanged.

### Added

- PostgreSQL job store (`storage.driver: postgres`, `SKIP LOCKED` leasing) and S3-compatible
  blob store (`blobs.driver: s3`), with `vde migrate` to apply migrations explicitly;
  SQLite and the filesystem stay the default.
- Provider adapters for AWS Bedrock (Converse API) and Google Vertex AI (Gemini and
  Anthropic models), using cloud IAM instead of API keys.
- Classifier judge adapter (`provider: classifier`, generic classify contract) with
  `roles.judge.feedback_fallback` (`whole_result` or `llm`) and `feedback_group`.
- Per-attempt group escalation (`roles.*.escalation`) and a second judge
  (`roles.second_judge`), both off by default.
- Image conversion: WebP converted to JPEG or PNG, EXIF orientation applied, oversized images
  downscaled (`documents.images`).
- `POST /v1/extract` (synchronous, `sync.max_wait`), signed completion webhooks
  (`applications[].webhook`, HMAC-SHA256 `X-VDE-Signature`, retries) and JSON submission with
  a base64 or guarded URL document (`documents.fetch`: HTTPS, host allow-list, address
  checks against private ranges).
- `vde eval`: evaluation harness with a corpus format, a TravelInbox importer, synthetic
  receipts and lab reports, and false accepts as the headline; results in `docs/quality.md`.
- Horizontal scaling: `vde serve --role all|api|worker`, an advisory-locked sweeper,
  `worker.lease`, queue age in `/healthz` on API-only processes, `rate.shared` (per-minute
  counters in the store, `rate_windows` table) and a scaled compose example
  (`examples/compose/scaled`: one API and two workers on PostgreSQL and RustFS).
- Provider token usage: `vde_provider_tokens_total`, `input_tokens` and `output_tokens` per
  job (admin API metadata, audit details, logs, `vde extract` output), and a `group` label
  on provider-call metrics.
- `max_output_tokens` per model (default 16384), a truncation signal from the adapters and
  cut-off feedback in the extraction loop.

### Changed

- The SQLite WAL is checkpointed (best effort) after every statement that deletes job
  content, so deleted content does not linger in the WAL.
- Replaying an `Idempotency-Key` for a job whose content was deleted answers with
  `content_deleted_at` set; the idempotency window has a 24-hour floor, kept until
  `created_at` + 24 h even when content expires earlier.
- `/healthz` is exempt from the HTTPS requirement, so a probe can reach the service directly.
- Worker ids are `<host>-<pid>-<random>`: in containers every process is pid 1, and the old
  ids let replicas renew each other's leases.

### Known gaps

- The Bedrock and Vertex adapters have not run against live endpoints (no credentials); do
  not treat them as supported until their opt-in smoke tests have passed.
- The classifier judge has not been evaluated against a vendor API (unavailable).
- HEIC is not converted (no pure-Go decoder); it is accepted only where an endpoint declares it.
- Postgres `LISTEN/NOTIFY` was skipped: workers poll. `rate.shared` keeps its counters in the
  job store; there is no Redis. Pending webhook deliveries live in memory and are lost on restart.
- The first TravelInbox evaluation shows a high false-accept rate that points at instruction
  and comparison-rule alignment (see `docs/quality.md` and ADR 0008).

## [0.2.0] - 2026-10-16

The service release: the v0.1 engine behind an authenticated HTTP API. Implemented, but not
yet verified live (see Known gaps).

### Added

- `vde serve`: HTTP service with `POST /v1/jobs` (multipart submission, idempotency keys),
  `GET` and `DELETE /v1/jobs/{id}`, `GET /v1/quota` and `GET /healthz`; graceful shutdown that
  drains requests and running jobs for up to 30 s.
- Applications and access tokens: applications declared in the configuration, tokens created,
  listed and revoked with `vde token`, applications listed with `vde app list`; scopes `jobs`
  and `jobs:read`, mandatory expiry, SHA-256 token hashes, HTTPS required outside loopback
  (`server.tls`, `server.trusted_proxy`, `server.insecure_http`).
- Jobs: SQLite store with migrations, filesystem blob store, a job state machine with leasing
  and a worker pool with per-job deadline; jobs of other applications answer `404`.
- Quota per application and window, per-application rate limits (requests, submits, jobs in
  progress), and upload limits.
- Retention: `none` and `default` with per-application `force_none` and
  `delete_blobs_at_terminal`; deletion at the terminal state, on first read and on `DELETE`;
  the expiry sweeper, which also finishes stale jobs and prunes quota, token and audit rows;
  SQLite `secure_delete`.
- Operations: `/healthz` with reason codes, Prometheus `/metrics` (optionally behind
  `metrics.token_env` or on `metrics.listen`), the audit log (`audit_events`, `vde audit
  list`, `audit.log`, `audit.retention`) with job events, redacted logging, and
  `vde healthcheck`.
- Admin API (`/admin/v1`) on its own listener (`admin.listen`, default `127.0.0.1:8091`) with
  admin tokens (`vde token create --admin`), 60 requests per minute per token, audited
  changes; OpenAPI documents for both APIs.
- Container image: multi-stage `Dockerfile` for `linux/amd64` and `linux/arm64` on
  `gcr.io/distroless/static:nonroot`, non-root, `vde` as entrypoint, `/var/lib/vde` as data
  volume, port 8090; `make image` and `make image-multiarch`.
- Docker compose example in `examples/compose/` with a development configuration, a commented
  Caddy TLS service and an optional Prometheus profile.
- Release automation: `.goreleaser.yaml` (static binaries for linux and darwin, amd64 and
  arm64, archives, checksums) and a release workflow publishing the image to
  `ghcr.io/kaanemec/vde` and the GitHub release on `v*` tags.
- Documentation: operator guide, API guide, security and privacy page with the operator
  checklist, provider pages, architecture decisions, TravelInbox conformance review.

### Known gaps

- The real-provider smoke test (`make live`) and the TravelInbox live evaluation have not been
  run.
- The multi-architecture image has been built only in CI, not verified on both architectures
  by hand.
- `vde config check --probe` is not implemented: no provider has a probe, so it only warns
  "probe not available" (planned for v0.3).

## [0.1.0] - 2026-10-16

The engine and the command line.

### Added

- Configuration: one YAML file (`endpoints`, `groups`, `roles`, limits, documents) with
  secrets only by environment-variable name, and `vde config check`, which validates it
  without network calls (`--strict`; the `--probe` flag exists but no provider has a probe).
- Provider adapters: Anthropic, OpenAI-compatible (with an OpenRouter preset, also Ollama,
  vLLM, Azure OpenAI), Google Gemini, and a scripted fake provider for tests; declared
  per-model `accepts` media types and data-handling notes.
- Endpoint groups with failover: bounded retries, error classification, endpoint health
  marking.
- Document representation: media types, native versus text versus page-image selection,
  raw or visible-text HTML, `.eml` bodies and attachment listing.
- The extraction loop: extractor and judge roles, answer parsing, JSON Schema validation
  fed back as feedback, judge verdicts, schema-less mode, and a `service_version` string
  describing configuration and prompts.
- `vde extract` runs one document through the loop and prints the job-shaped result
  (exit codes 0 accepted, 2 rejected, 3 failed, 1 usage or configuration error);
  `vde version`; `vde help`.
- Examples: five ready-to-edit configurations and synthetic sample documents; end-to-end
  scenarios with the fake provider (`make scenarios`); an opt-in live-provider smoke test
  (`make live`).
- Continuous integration: tests with the race detector on two Go versions, golangci-lint,
  `govulncheck`, and a static cross-build for linux/amd64 and linux/arm64; Dependabot.

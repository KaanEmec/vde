# Changelog

All notable changes to this project are recorded here, in the format of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project follows
[Semantic Versioning](https://semver.org/). Release notes on GitHub link to this file.

## [Unreleased]

## [0.4.0] - 2026-10-18

Judge policy, a deterministic grounding check and PDF preparation in the service
(ADR 0009, ADR 0010). Under the default judge policy `always` the TravelInbox contract
holds exactly: the conformance suite passes unchanged. The other policies are opt-in
supersets. The owner's policy comparison has not been run yet (see Known gaps).

### Added

- Judge policy `always` (default), `never` or `auto`: `roles.judge.policy`, per application
  `applications[].judge.policy`, `judge.sample_rate` (default 0.05) and
  `judge.allow_override`, which lets a request choose with the `judge` part or JSON field
  (`400 bad_request` otherwise). Applications store `judge_policy`, `judge_allow_override`
  and `judge_sample_rate` (migration 0009), settable over the admin API; an application
  that allows override has one `service_version` per policy.
- Terminal status `extracted` (judge policy `never`: a schema-valid result no judge looked
  at; never acceptance) and the job field `verification` (`judge`, `grounded` or `none`;
  `null` while in progress). The job store gains `judge_policy` and `verification`
  (migration 0008); the admin job list accepts `status=extracted`.
- `internal/ground`: the deterministic grounding check. Every string, number and ISO date
  leaf of a candidate must occur in the normalized document text (casefolding, diacritics,
  thousands separators and decimal conventions, a documented date and time table in six
  languages, rules version `ground-1`). Schemas steer it with
  `x-vde-grounding: critical | check | derived | skip`; misses are feedback entries with
  `source: "grounding"` that never carry the value.
- Judge policy in the loop: `never` ends `extracted` with advisory grounding entries;
  `auto` accepts without a judge (`verification: grounded`) when the document has text, at
  least one critical leaf was checked and all are found, and the candidate answers no
  schema or judge feedback; a deterministic sample (FNV-1a of the job id, of the document
  bytes in the CLI) is judged anyway. Metrics `vde_judge_decisions_total{policy, decision}`
  and `vde_grounding_disagreements_total`; `job.finished` audit details carry
  `verification` and `judge_policy`.
- `documents.pdf` (`mode: auto | native | text | pages`, `dpi`, `max_pages`,
  `min_text_chars_per_page`, `workers`) and `applications[].documents.pdf.mode`: when no
  group reads `application/pdf` natively, the service sends a PDF's text layer or its
  rendered pages instead of refusing it. The text layer is also the PDF's grounding text.
- `internal/pdf`: pdfium compiled to WebAssembly (go-pdfium on wazero), cgo-free, lazily
  started, one bounded pool per process, a fresh sandboxed instance per document (no
  filesystem, no network, 1 GiB memory cap, 30 s per call), compilation cache under
  `server.data_dir/pdfium-cache`. Submit, the workers, `vde extract` and `vde eval` share
  the selection.
- `vde eval`: per case `judge_policy`, `verification`, `judge_calls`, `decision`,
  `disagreements` (and `error_code`); totals for extracted cases and their field accuracy,
  false accepts by verification (`judge` or `grounded`), cases per verification, the
  grounded share, judge calls, sampled cases and disagreements, all covered by
  `--compare`. `--judge always|never|auto` runs a corpus under one policy (sent as the
  `judge` part with `--url`), and `--cases` takes a comma-separated list of globs.
  `make eval` runs the corpus once per policy.
- `vde extract --judge always|never|auto`; exit code 4 for `extracted`.
- Documentation: [judge policy](docs/operator/judge-policy.md) (policies, grounding, schema
  annotations, the date table, metrics), [PDF preprocessing](docs/operator/pdf.md), the
  `extracted` status, `verification` and `source: "grounding"` in the API guide, the
  pdfium sandbox in the security model, and docs tests that keep the configuration
  reference and the date table in step with the code.

### Changed

- [ADR 0006](docs/architecture/decisions/0006-representation-policy.md) is amended by
  [ADR 0010](docs/architecture/decisions/0010-pdf-preprocessing-in-service.md): under the
  default `documents.pdf.mode: auto` a PDF without pages for models that do not read PDFs
  is no longer refused with `415` (contract 4.11) but prepared by the service.
  `documents.pdf.mode: native` keeps the v0.3 behaviour; the conformance suite's instance
  without a PDF reader runs it.
- `vde config check` no longer warns that a deployment "depends on callers sending pages"
  when a group cannot read PDFs: it prints how each role gets a PDF under the mode, warns
  only under `native` or when a group reads PDFs only through their text layer, prints the
  pdfium memory estimate, and warns when wazero would interpret instead of compile.
- Under `always` and `auto` a schema-valid candidate whose critical values are not in the
  document goes back to the extractor once before any judge call (one extractor call, no
  judge call, never on the last attempt; whether it was made is stored with the job).
  Under `always` the grounding entries reach only the extractor, never the job's
  `feedback`, so TravelInbox sees contract-v1 sources only.
- Every `service_version` changes once with v0.4: the grounding rules (`ground-1`) are
  hashed under every policy (the repair under `always` changes results), and the PDF policy
  (mode, dpi, minimum text characters) where it can change what is read.
- `vde eval --url` treats `extracted` as terminal.
- Dependencies (direct since `go mod tidy`): `github.com/klippa-app/go-pdfium` v1.21.3
  (MIT) and `github.com/tetratelabs/wazero` v1.12.0 (Apache-2.0), with pdfium itself
  (BSD-3-Clause) embedded as WebAssembly and `github.com/jolestar/go-commons-pool/v2`
  v2.1.2 (Apache-2.0) indirectly; licenses in `NOTICE`, which the image carries next to
  `LICENSE`.
- Binary size (`CGO_ENABLED=0`, `-trimpath -s -w`): linux/amd64 35.9 MB (v0.3: 26.8 MB,
  +9.2 MB), linux/arm64 34.2 MB (v0.3: 25.2 MB, +9.0 MB), darwin/arm64 34.9 MB. The image
  is the binary plus about 2 MB of distroless/static (not rebuilt for this release).

### Known gaps

- The owner's policy comparison (`always`, `auto`, `never` on the owner suite and the
  TravelInbox corpus) has not been run: the v0.4 exit criteria 1 to 3 on real documents
  and models are pending (`docs/quality.md`, "Judge policy"). Automated tests cover them
  with the fake provider.
- pdfium runs without system fonts: a PDF whose fonts are neither embedded nor among the
  14 standard ones may render with fallback glyphs (the text layer is unaffected).
- Memory: each pdfium instance takes about 230 to 490 MB while it prepares a document
  (capped at 1 GiB); budget about 500 MB per `documents.pdf.workers`. The first PDF of a
  process without a writable compilation cache waits about 1.2 s.
- wazero compiles only on amd64 and arm64; elsewhere pdfium is interpreted, about 60 times
  slower.
- No OCR: scanned PDFs are rendered as pages and are not groundable; images are never
  groundable, so `auto` sends them to the judge.
- Grounding cannot catch a printed value in the wrong field, a wrong choice among printed
  values or an omission; `auto` measures that risk through its sample.
- The container image size was not measured for this release (no buildx on the build
  machine).

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

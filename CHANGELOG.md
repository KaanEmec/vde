# Changelog

All notable changes to this project are recorded here, in the format of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project follows
[Semantic Versioning](https://semver.org/). Release notes on GitHub link to this file.

## [Unreleased]

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

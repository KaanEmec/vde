# Changelog

All notable changes to this project are recorded here, in the format of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project follows
[Semantic Versioning](https://semver.org/). Release notes on GitHub link to this file.

## [Unreleased]

### Added

v0.2, the service (in progress):

- Job store: SQLite with migrations, a filesystem blob store, a job state machine with
  leasing, and a worker pool with a per-job deadline (epic 03, done).
- Container image: multi-stage `Dockerfile` for `linux/amd64` and `linux/arm64` on
  `gcr.io/distroless/static:nonroot`, running as a non-root user, `vde` as entrypoint,
  `/var/lib/vde` as the data volume, port 8090; `make image` and `make image-multiarch`.
- Docker compose example in `examples/compose/` with a development configuration, a
  commented Caddy TLS service and an optional Prometheus profile.
- Release automation: `.goreleaser.yaml` (static binaries for linux and darwin, amd64 and
  arm64, archives, checksums) and `.github/workflows/release.yml` publishing the image to
  `ghcr.io/kaanemec/vde` and the GitHub release on `v*` tags.

v0.1, the engine and the command line:

- Configuration: one YAML file (`endpoints`, `groups`, `roles`, limits, documents) with
  secrets only by environment-variable name, and `vde config check`, which validates it
  without network calls (`--strict`, `--probe`).
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

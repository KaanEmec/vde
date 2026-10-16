# Job: Configuration validation and `vde config check`

Version: v0.1 · Epic: 01 Project foundation · Status: done · Depends on: 02

## Goal

Every configuration mistake is caught at start or by `vde config check`, with a message
naming the key and the rule, and without network calls unless `--probe` is given.

## Context

Brief: "Validate configuration early, including document capabilities and allowed
failover destinations." Contract §12: "`vde config check` MUST validate it without network
calls beyond an optional endpoint probe: every group references defined endpoints, every
endpoint-model pair declares the document media types it accepts, and both roles have a
group."

## Tasks

- [x] Validation rules, each with a test:
  - every group entry references a defined endpoint and a model declared on it;
  - every endpoint-model pair has a non-empty `accepts` list of known media types;
  - both roles reference a defined group; a group may serve both roles;
  - the extractor group can read, directly or via page images (`image/png`,
    `image/jpeg`), every MUST media type of contract §4; otherwise a warning listing what
    cannot be processed (error with `--strict`); in particular warn when no extractor
    pair reads `application/pdf` natively, since the service never renders pages
    (ADR 0006) and such a deployment depends on callers sending `page` images;
  - `limits.job_timeout` ≤ 8m, `max_attempts` ≥ 1, failover bounds positive;
  - `api_key_env` variables are set when the provider needs a key (error) and empty is
    allowed only for `openai_compatible` endpoints;
  - `debug.log_content: true` together with a non-loopback `server.listen` is an error;
  - application settings (v0.2) validated when present: unique ids, positive quotas.
- [x] `vde config check [--config path] [--probe] [--strict]`: prints a summary (endpoints,
      groups, roles, which media types each role can read, computed `service_version`
      once that job exists) and exits 0, or lists all errors and exits 1. Never prints
      secrets.
- [ ] `--probe` makes one cheap request per endpoint (a models list or a one-token
      completion) and reports reachability; failures are warnings, not errors.
      (Flag and hook done: `probes` in `cmd/vde/config.go`; each adapter job adds its probe.)
- [ ] `serve` and `extract` run the same validation at start and refuse to start on errors.
      (Shared `loadConfig` in `cmd/vde/config.go`; used when those commands land.)

## Acceptance criteria

- A table-driven test covers every rule with a failing and a passing configuration.
- `vde config check` on the dev example (epic 06) passes; a deliberately broken copy lists
  all errors at once, not just the first.

## Out of scope

Endpoint probing beyond reachability; validating model names against provider catalogs.

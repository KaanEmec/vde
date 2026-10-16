# Job: `service_version`

Version: v0.1 · Epic: 05 Extraction loop · Status: done · Depends on: 01, 01-02, 04-02

## Goal

A stable string identifying the release and the effective role configuration, so two
results with the same string came from the same code, models, failover, framings and
representation policy.

## Context

Contract §11: `<service>-<semver>+<8 hex of a hash over the effective role configuration>`,
for example `vde-1.4.0+c0ffee12`; MUST be in the job body and the `X-Service-Version`
header (v0.2); the representation choice is part of it (§4); escalation and attempt limit
are part of it (§6, §11).

## Tasks

- [x] `config.ServiceVersion(build, cfg) string`: SHA-256 over a canonical JSON of:
      groups (ordered endpoint provider type, base URL host, model name, `accepts`,
      failover bounds), roles (group per role), `limits.max_attempts`,
      `limits.job_timeout`, `documents.html` policy (raw_html or visible_text), framing texts and separators, the
      answer-parser leniency version constant, and the schema-validator library version.
      Exclude secrets, listen addresses, data paths and application settings.
- [x] Take the first 8 hex characters; `<semver>` from build info (`v0.1.0` → `0.1.0`;
      a dev build uses `0.0.0-dev`).
- [ ] Computed once at start, logged, printed by `vde config check`, returned by the
      CLI and (v0.2) set on every job at creation and sent in the header.
- [x] Per-application attempt limits (v0.2) change the effective configuration per
      application: compute the hash per application when applications are present, with
      the application's attempt limit replacing the default. Document this.
- [x] Tests: same config → same hash; changing a model, the HTML policy, a framing or
      the attempt limit changes it; changing a secret, a listen address or a quota does not.

## Acceptance criteria

- Hash stability tests pass; the string matches the contract's form.

## Out of scope

Exposing the configuration itself to callers (never).

## Implementation notes

Location deviation: implemented as `extract.ServiceVersion(cfg, buildVersion)` in
`internal/extract/version.go`, not in `internal/config`, because it hashes the framing
texts and `internal/extract` already imports `internal/config` (an import cycle
otherwise). Details:

- Each role's group is hashed by content (its ordered pairs and failover bounds), not by
  name, so unused groups and renames do not change the hash. Accepted media types are
  sorted; the base URL contributes its host only; endpoint timeouts are excluded.
- `limits.judge_reparse_tries` and `limits.feedback_message_chars` are hashed too, since
  they change results.
- The schema library version comes from `runtime/debug` build info (`unknown` when absent).
- Per application (v0.2): the doc comment says to pass a copy of the configuration with the
  application's attempt limit and HTML policy.
- Logging it at start, printing it in `vde config check` and returning it from the CLI are
  wired by epic 06 (left open here).

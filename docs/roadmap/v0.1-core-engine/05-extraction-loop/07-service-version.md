# Job: `service_version`

Version: v0.1 · Epic: 05 Extraction loop · Status: planned · Depends on: 01, 01-02, 04-02

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

- [ ] `config.ServiceVersion(build, cfg) string`: SHA-256 over a canonical JSON of:
      groups (ordered endpoint provider type, base URL host, model name, `accepts`,
      failover bounds), roles (group per role), `limits.max_attempts`,
      `limits.job_timeout`, `documents.html` policy (raw_html or visible_text), framing texts and separators, the
      answer-parser leniency version constant, and the schema-validator library version.
      Exclude secrets, listen addresses, data paths and application settings.
- [ ] Take the first 8 hex characters; `<semver>` from build info (`v0.1.0` → `0.1.0`;
      a dev build uses `0.0.0-dev`).
- [ ] Computed once at start, logged, printed by `vde config check`, returned by the
      CLI and (v0.2) set on every job at creation and sent in the header.
- [ ] Per-application attempt limits (v0.2) change the effective configuration per
      application: compute the hash per application when applications are present, with
      the application's attempt limit replacing the default. Document this.
- [ ] Tests: same config → same hash; changing a model, the HTML policy, a framing or
      the attempt limit changes it; changing a secret, a listen address or a quota does not.

## Acceptance criteria

- Hash stability tests pass; the string matches the contract's form.

## Out of scope

Exposing the configuration itself to callers (never).

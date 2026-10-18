# v1.0 Stable

Status: done 2026-10-18 (epics 01 and 02; 03-01 and epic 04 deferred by the owner) · Depends on: v0.2 (required), v0.3 and v0.4 (whatever has shipped)

## Goal

Promise stability: `/v1` and the configuration schema are frozen with a 12-month
compatibility commitment, the service has had a security review, its performance envelope
is measured and documented, and releases are signed and reproducible.

## Exit criterion

Tag `v1.0.0` with: a published compatibility policy, a frozen configuration schema and a
closed security review with no open high findings.

Owner's decision (2026-10-18): the performance baseline (03-01) and release engineering
(04) are not part of v1.0. The backpressure job (03-02) was already done and ships. The
drafts for the load tool, signed releases with SBOMs and the documentation site are kept
on branch `wip/release-engineering` and listed in the [backlog](../backlog.md).

## Exit criterion check (2026-10-18)

- Compatibility policy: [docs/compatibility.md](../../compatibility.md); the API document is
  frozen as `api/openapi.v1.0.0.yaml` with `TestOpenAPISupersetOfSnapshot` guarding it.
- Configuration schema: `internal/config/schema.json` (`vde config schema`), every example
  validates; no key renamed, so no migration tooling.
- Security review: the hardening pass and an independent review are recorded in
  [docs/security.md](../../security.md); every medium or higher finding is fixed, the
  release toolchain is pinned (`toolchain` directive in `go.mod`).

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [API and configuration freeze](01-api-freeze/README.md) | v0.2, v0.3 decisions | done |
| 02 | [Security review](02-security-review/README.md) | v0.2 | done |
| 03 | [Performance baseline](03-performance/README.md) | v0.2 | 02 done; 01 deferred |
| 04 | [Release engineering](04-release-engineering/README.md) | v0.2 08-03 | deferred |

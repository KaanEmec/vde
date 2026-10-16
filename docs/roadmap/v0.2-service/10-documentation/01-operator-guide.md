# Job: Operator guide

Version: v0.2 · Epic: 10 Documentation · Status: in progress · Depends on: epics 01–08

## Goal

`docs/operator/`: install, configure, run, upgrade and troubleshoot the service without
reading code.

## Context

Brief: "documented configuration for roles, endpoints, failover, credentials, and access
control." Contract §12 (minimal development configuration documented).

## Tasks

- [x] `install.md`: binary, container, compose; data directory; TLS or reverse proxy.
- [x] `configuration.md`: every key with type, default and meaning (finishes the
      placeholder from v0.1), with the three example files explained.
- [x] `providers.md` index (from v0.1) plus guidance on choosing models per role and
      declaring `accepts`.
- [x] `applications-and-tokens.md`: lifecycle, rotation procedure, revocation, scopes,
      CLI and admin API side by side (the admin API is marked planned, epic 11).
- [x] `retention.md`, `privacy.md` (from epic 06), `operations.md` (healthz, metrics,
      audit, logs, backups of the SQLite file and blob directory, upgrade and migration
      notes).
- [x] `troubleshooting.md`: the error codes an operator sees and what to check, common
      provider failures, `config check` messages.

## Notes

Written against the code on 2026-10-16: `docs/operator/` now has `README.md` (index),
`install.md`, `configuration.md`, `providers.md`, `applications-and-tokens.md`,
`operations.md` and `troubleshooting.md` beside the existing `retention.md`, `privacy.md`
and `framings.md`. Planned and marked as such: the admin API (epic 11), `vde audit list`
(epic 07 job 03) and `config check --probe` (no provider has a probe yet).

## Acceptance criteria

- [ ] Someone unfamiliar with the project deploys with compose, creates a token and processes
  a document following only the docs (a reviewer tries it).

## Out of scope

A documentation website (v1.0 release engineering may add one).

Status stays `in progress`: the acceptance criterion needs a reviewer who follows the docs
from a clean machine, which cannot be done by the author.

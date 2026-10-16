# Job: Audit log

Version: v0.2 · Epic: 07 Operations · Status: planned · Depends on: 03-01, 02-02

## Goal

An append-only record of security-relevant and per-job events, without content, in the
database and optionally as a log stream.

## Context

Contract §12: token creation, rotation and revocation; per job: application, job id,
`created_at`, media type and size, page count, terminal status, attempts, `service_version`,
error code; never content. Brief: "Record security-relevant access and administrative
events without exposing secrets."

## Tasks

- [ ] `audit_events(id, at, kind, application_id, actor, job_id, details JSON)` with a
      fixed set of kinds: `token.created`, `token.revoked`, `app.enabled`,
      `app.disabled`, `job.created`, `job.finished`, `job.deleted`, `job.expired`,
      `auth.failed` (with reason code, no token), `config.loaded` (with
      `service_version`).
- [ ] `details` schema per kind defined as Go structs so no free-form data is written.
- [ ] Also emitted to `slog` at `info` with `audit=true` when `audit.log: true`.
- [ ] `vde audit list --app id --since …` CLI for operators without a database client.
- [ ] Retention of audit rows is separate (`audit.retention`, default 365 days), swept
      by the sweeper.
- [ ] Tests: every kind written by the code path that owns it; the redaction sentinel
      test (epic 06) covers the audit table too.

## Acceptance criteria

- Every event in the contract's list appears in the table after the scenario suite.

## Out of scope

Tamper evidence (hash chains), shipping to external SIEMs (use the log stream).

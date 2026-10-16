# Job: Audit log

Version: v0.2 · Epic: 07 Operations · Status: in progress · Depends on: 03-01, 02-02

## Goal

An append-only record of security-relevant and per-job events, without content, in the
database and optionally as a log stream.

## Context

Contract §12: token creation, rotation and revocation; per job: application, job id,
`created_at`, media type and size, page count, terminal status, attempts, `service_version`,
error code; never content. Brief: "Record security-relevant access and administrative
events without exposing secrets."

## Tasks

- [x] `audit_events(id, at, kind, application_id, actor, job_id, details JSON)` with a
      fixed set of kinds: `token.created`, `token.revoked`, `app.enabled`,
      `app.disabled`, `job.created`, `job.finished`, `job.deleted`, `job.expired`,
      `auth.failed` (with reason code, no token), `config.loaded` (with
      `service_version`).
- [x] `details` schema per kind defined as Go structs so no free-form data is written.
- [x] Also emitted to `slog` at `info` with `audit=true` when `audit.log: true`. (`audit.Options.Logger`; config key `audit.log`, wired in `serve`.)
- [ ] `vde audit list --app id --since …` CLI for operators without a database client.
- [x] Retention of audit rows is separate (`audit.retention`, default 365 days), swept
      by the sweeper (`sweeper.Options.AuditRetention`; config key `audit.retention`, wired in `serve`).
- [ ] Tests: every kind written by the code path that owns it; the redaction sentinel
      test (epic 06) covers the audit table too.

## Notes

Done: package `internal/audit` (`Kind` set, one details struct per kind, `Recorder`,
`Decode`), `job.Store.ListAudit` and `DeleteAuditBefore`, `job.finished` from the worker and
`job.expired`/`job.finished` from the sweeper. Remaining call sites, for the api/cmd step:
`job.created` (submit handler, `Recorder.JobCreated`), `job.deleted` (`DELETE` and first
read, `Recorder.JobDeleted`), `token.created`/`token.revoked`/`app.enabled`/`app.disabled`
(cmd, now written with ad-hoc structs; switch to `Recorder.Record` with the audit structs),
`auth.failed`, `config.loaded` at startup, the `audit.log` and `audit.retention` config
keys, the `vde audit list` command (`ListAudit` with `job.AuditFilter`, `audit.Decode`),
and adding `audit`, `actor` and `details` to the logging allowed keys (the redacting
handler drops them today). Status stays `in progress`.

## Acceptance criteria

- Every event in the contract's list appears in the table after the scenario suite.

## Out of scope

Tamper evidence (hash chains), shipping to external SIEMs (use the log stream).

Update (epic 05/07 wiring): `config.loaded` is written at `serve` start; `job.deleted` by
`DELETE` (`request`) and the first terminal read (`first_read`); `auth.failed` now goes
through `audit.Recorder` with `audit.AuthFailed`; `serve` passes recorders to the API,
the worker and the sweeper. Still ad hoc: the token and app events in `cmd/vde`.

# Job: Audit log

Version: v0.2 · Epic: 07 Operations · Status: done · Depends on: 03-01, 02-02

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
- [x] `vde audit list --app id --since …` CLI for operators without a database client
      (`cmd/vde/audit.go`: `--app`, `--since` duration or RFC 3339, default 24h, `--kind`,
      `--limit`, default 100; the admin API serves the same at `GET /admin/v1/audit`).
- [x] Retention of audit rows is separate (`audit.retention`, default 365 days), swept
      by the sweeper (`sweeper.Options.AuditRetention`; config key `audit.retention`, wired in `serve`).
- [x] Tests: every kind written by the code path that owns it (`cmd/vde`
      `TestAuthEndToEnd` and `TestAdminEndToEnd`, `internal/sweeper`, `internal/worker`,
      `internal/api` admin and jobs tests); the sentinel tests of the worker, sweeper and
      API check the audit details carry no content.

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

Done (epic 11 agent): the token and app events in `cmd/vde` now go through
`audit.Recorder` with the typed structs (`audit.TokenCreated`, `TokenRevoked`,
`AppEnabled`, `AppDisabled`); `syncApplications` records through the serve recorder, so
`audit.log` covers them too. The admin API writes the same kinds with actor
`admin:<token id>` (`audit.ActorAdmin`) and `job.deleted` reason `admin`
(`audit.DeletedByAdmin`). `audit.ParseSince` is shared by the CLI and the admin API.
Deviation: `token.created` details carry `expires_at` with sub-second precision (the
struct's `time.Time`) instead of the CLI's former whole-second string.

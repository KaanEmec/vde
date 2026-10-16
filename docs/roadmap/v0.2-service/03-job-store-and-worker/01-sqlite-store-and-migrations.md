# Job: SQLite store and migrations

Version: v0.2 · Epic: 03 Job store and worker · Status: done · Depends on: v0.1 01-01

## Goal

`internal/job.Store` interface with a SQLite implementation, embedded numbered migrations
applied at start, and the tables of the architecture data model.

## Context

ADR 0002. Architecture overview, "Data model (v0.2)".

## Tasks

- [x] Driver `modernc.org/sqlite`; pragmas: WAL, `busy_timeout` 5 s, `foreign_keys` on,
      `synchronous NORMAL`. One `*sql.DB` with `MaxOpenConns` small (writes serialize).
- [x] Migrations as embedded `NNNN_name.sql` files with a `schema_migrations` table;
      applied in a transaction each at `serve`, `token`, `app` start.
- [x] Tables: `applications`, `tokens`, `jobs`, `quota_usage`, `audit_events`,
      `idempotency_keys` (or columns on `jobs`; choose columns plus a unique index
      `(application_id, idempotency_key)` to stay simple).
- [x] Store interface, kept to what the service needs: `CreateJob`, `GetJob(app, id)`,
      `LeaseNextQueued(workerID, until)`, `RecordAttempt(id, progress)`, `Finish(id,
      outcome)`, `MarkContentDeleted`, `DeleteJob`, `FindByIdempotencyKey`,
      `ListExpired(now)`, plus token and application methods and quota methods used by
      epics 02 and 05. No generic repository.
- [x] `result`, `feedback`, `error` stored as JSON text columns; sizes bounded by the
      upload limits and the feedback cap.
- [x] Tests against a temp database for each method; a migration test applies all from
      scratch and from each intermediate version.

## Acceptance criteria

- The interface has only methods with a caller; `go vet` plus a test that the Postgres
  implementation (v0.3) can be written without new methods is a design review, not code.

## Out of scope

Postgres (v0.3), encryption inside the database file (host volume encryption, ADR 0002).

## Outcome

Done in `internal/job` (`job.go`, `sqlite.go`, `migrate.go`, `migrations/`). Migrations
`0001_applications_and_tokens`, `0002_jobs`, `0003_quota_and_audit` (STRICT tables, times
as Unix milliseconds, `_txlock=immediate` so a concurrent CLI process serializes on BEGIN).
Deviations:

- Store methods take the caller's clock (`now`) so tests and the sweeper control time.
- `IncrementQuota(app, windowStart, limit)` takes the limit and returns
  `ErrQuotaExhausted` (the `… WHERE count < limit RETURNING count` upsert of epic 05).
- Added `RenewLease` (worker heartbeat, see job 03) and two content columns:
  `inputs_deleted_at` (document, pages, instructions, schema gone at the terminal state)
  besides `content_deleted_at` (result, feedback, error gone at the first terminal read),
  so a `retention: none` job stays readable once between the two.
- Audit `details` is validated JSON text; `ListExpired` returns full rows.

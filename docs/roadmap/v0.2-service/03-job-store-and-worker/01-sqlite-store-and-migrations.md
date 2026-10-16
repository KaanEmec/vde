# Job: SQLite store and migrations

Version: v0.2 · Epic: 03 Job store and worker · Status: planned · Depends on: v0.1 01-01

## Goal

`internal/job.Store` interface with a SQLite implementation, embedded numbered migrations
applied at start, and the tables of the architecture data model.

## Context

ADR 0002. Architecture overview, "Data model (v0.2)".

## Tasks

- [ ] Driver `modernc.org/sqlite`; pragmas: WAL, `busy_timeout` 5 s, `foreign_keys` on,
      `synchronous NORMAL`. One `*sql.DB` with `MaxOpenConns` small (writes serialize).
- [ ] Migrations as embedded `NNNN_name.sql` files with a `schema_migrations` table;
      applied in a transaction each at `serve`, `token`, `app` start.
- [ ] Tables: `applications`, `tokens`, `jobs`, `quota_usage`, `audit_events`,
      `idempotency_keys` (or columns on `jobs`; choose columns plus a unique index
      `(application_id, idempotency_key)` to stay simple).
- [ ] Store interface, kept to what the service needs: `CreateJob`, `GetJob(app, id)`,
      `LeaseNextQueued(workerID, until)`, `RecordAttempt(id, progress)`, `Finish(id,
      outcome)`, `MarkContentDeleted`, `DeleteJob`, `FindByIdempotencyKey`,
      `ListExpired(now)`, plus token and application methods and quota methods used by
      epics 02 and 05. No generic repository.
- [ ] `result`, `feedback`, `error` stored as JSON text columns; sizes bounded by the
      upload limits and the feedback cap.
- [ ] Tests against a temp database for each method; a migration test applies all from
      scratch and from each intermediate version.

## Acceptance criteria

- The interface has only methods with a caller; `go vet` plus a test that the Postgres
  implementation (v0.3) can be written without new methods is a design review, not code.

## Out of scope

Postgres (v0.3), encryption inside the database file (host volume encryption, ADR 0002).

# Job: Postgres job store

Version: v0.3 · Epic: 01 Storage backends · Status: done · Depends on: v0.2 03-01

## Goal

`storage: {driver: postgres, dsn_env: VDE_DATABASE_URL}` runs the same store interface
on PostgreSQL, enabling multiple replicas and managed databases.

## Context

ADR 0002 (interface sized for a second implementation). Epic 07 needs row-level leasing
across processes.

## Tasks

- [x] `pgx` stdlib driver; migrations as a parallel set of SQL files (dialect
      differences are small: `RETURNING` exists, `ON CONFLICT` exists, timestamps
      `timestamptz`, JSON as `jsonb`).
- [x] Leasing with `FOR UPDATE SKIP LOCKED` so replicas never lease the same job.
- [x] The store test suite from v0.2 runs against both drivers (Postgres via a
      `testcontainers` or a CI service container; skipped locally without
      `VDE_TEST_POSTGRES`).
- [x] `vde migrate` subcommand for operators who prefer explicit migrations.
- [x] Operator docs: when to choose Postgres, connection pool settings, backups.

## Acceptance criteria

- Shared store test suite passes on both drivers; no caller changed.

## Out of scope

Other databases (MySQL): not planned.

## Outcome

- `internal/job/store.go` holds one `database/sql` implementation shared by both drivers;
  statements are written once with SQLite placeholders and rewritten to `$N` for
  PostgreSQL. Only two statements differ (JSON table functions in
  `DisableApplicationsNotIn`, and the lease's `FOR UPDATE SKIP LOCKED`). `SQLite`
  (`job.Open`, unchanged for callers) and `Postgres` (`job.OpenPostgres`) embed it.
- `internal/job/migrations_postgres/0001`–`0005` mirror the SQLite files version for
  version; concurrent migrators serialize on an advisory lock.
- `storage: {driver, dsn_env, max_open_conns}` in the configuration; `cmd/vde` opens the
  store through one helper (`openStore`) for `serve`, `app`, `token`, `audit` and
  `migrate`. Errors redact the DSN's password.
- Tests: every store test runs as a `sqlite` and a `postgres` subtest (fresh schema per
  test), plus a schema-equivalence test from every intermediate version, concurrent
  migration and concurrent leasing tests, and an end-to-end `serve` on PostgreSQL and S3
  in `cmd/vde`. CI job `storage` runs them against a `postgres:17-alpine` service.
- Docs: [storage](../../../operator/storage.md), configuration rows.

## Deviations

- Times are `BIGINT` Unix milliseconds, not `timestamptz`: the same representation as
  SQLite keeps one scan path and one set of statements.
- `result`, `feedback`, `error` and audit `details` are `json`, not `jsonb`: `jsonb`
  rewrites the text (key order, spacing) and the service returns the candidate byte for
  byte. Only `attachments` is `jsonb`.
- No `testcontainers`: a CI service container and `VDE_TEST_POSTGRES` locally.
- `serve` still migrates at start; there is no switch that makes it refuse pending
  migrations. `vde migrate` is the explicit path.
- `max_in_progress` is not locked across replicas: concurrent leases may exceed it by the
  number of replicas leasing at the same instant (documented).

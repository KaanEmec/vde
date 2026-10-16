# Job: Postgres job store

Version: v0.3 · Epic: 01 Storage backends · Status: in progress · Depends on: v0.2 03-01

## Goal

`storage: {driver: postgres, dsn_env: VDE_DATABASE_URL}` runs the same store interface
on PostgreSQL, enabling multiple replicas and managed databases.

## Context

ADR 0002 (interface sized for a second implementation). Epic 07 needs row-level leasing
across processes.

## Tasks

- [ ] `pgx` stdlib driver; migrations as a parallel set of SQL files (dialect
      differences are small: `RETURNING` exists, `ON CONFLICT` exists, timestamps
      `timestamptz`, JSON as `jsonb`).
- [ ] Leasing with `FOR UPDATE SKIP LOCKED` so replicas never lease the same job.
- [ ] The store test suite from v0.2 runs against both drivers (Postgres via a
      `testcontainers` or a CI service container; skipped locally without
      `VDE_TEST_POSTGRES`).
- [ ] `vde migrate` subcommand for operators who prefer explicit migrations.
- [ ] Operator docs: when to choose Postgres, connection pool settings, backups.

## Acceptance criteria

- Shared store test suite passes on both drivers; no caller changed.

## Out of scope

Other databases (MySQL): not planned.

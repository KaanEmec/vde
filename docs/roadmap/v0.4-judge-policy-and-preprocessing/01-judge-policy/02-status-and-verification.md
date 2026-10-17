# Job: `extracted` status and `verification` field

Version: v0.4 · Epic: 01 Judge policy · Status: done · Depends on: 01

## Goal

The job model, both stores, the job JSON, the OpenAPI documents, webhooks and the admin API
know the terminal status `extracted`, the field `verification` and the stored effective
`judge_policy`.

## Context

Contract §5: TravelInbox treats every status it does not know as not accepted, so
`extracted` is additive. `verification` is `judge`, `grounded` or `none` on terminal jobs and
`null` while queued or running. With the default policy, `accepted` always pairs with
`judge`, and `rejected` and `failed` with `none`.

## Tasks

- [x] `internal/extract/extract.go`: `StatusExtracted`, constants `VerificationJudge`,
      `VerificationGrounded`, `VerificationNone`, and `Outcome.Verification string`. The
      v0.3 loop sets `judge` on accept and `none` otherwise. Feedback source constant
      `SourceGrounding = "grounding"`.
- [x] `internal/job/job.go`: `StatusExtracted`, with `Terminal()` including it. `Job` gains
      `JudgePolicy` and `Verification`. The state-machine tests cover the new edge
      `running → extracted`.
- [x] Migrations `internal/job/migrations/0008_judge_policy.sql` and
      `internal/job/migrations_postgres/0008_judge_policy.sql`: `judge_policy TEXT NOT NULL
      DEFAULT 'always'`, `verification TEXT NULL`. Existing terminal rows read as
      `verification` derived from status (`accepted` → `judge`, else `none`) in the store,
      not by a data migration.
- [x] `internal/job/sqlite.go`, `postgres.go`: write and read both columns. The
      idempotency replay carries them.
- [x] `internal/api/jobjson.go`: emit `verification` always (`null` while in progress).
      `internal/api/sync.go` and `internal/webhook` treat `extracted` as terminal. The
      webhook body carries `status` as is.
- [x] `api/openapi.yaml`: `Job.status` enum gains `extracted`, `Job.verification`
      (`enum: [judge, grounded, none]`, nullable), and `FeedbackEntry.source` gains
      `grounding`. Admin OpenAPI: job metadata gains `judge_policy` and `verification`.
      `internal/api/openapi_test.go` keeps the documents and handlers in step.
- [x] `internal/metrics`: the job outcome metric accepts `status="extracted"`.

## Acceptance criteria

- Store tests on SQLite and Postgres round-trip both fields. A v0.3 row reads as
  `verification: judge` when accepted and `none` otherwise.
- `internal/api/jobs_get_test.go`: `verification` is `null` for queued and running jobs and
  present on every terminal job. A job stored as `extracted` is served as such, and
  `retention: none` deletes its content on the first terminal read like any other terminal
  job.
- The conformance suite passes unchanged.

## Out of scope

Producing `extracted` or `grounded` (epic 03); the request part (job 03).

## Result (2026-10-17)

Done. Migrations `0008_judge_policy.sql` (SQLite rebuilds `jobs`, since a `CHECK` cannot be
changed in place; PostgreSQL swaps `jobs_status_check`) add `judge_policy` and
`verification` with `CHECK`s. `job.Statuses`, `job.DefaultVerification(status)`,
`job.DefaultJudgePolicy` and the `job.Verification*` constants are new. `Finish` stores
`Outcome.Verification`, or the status-derived value when it is empty (sweeper and
`fromRow` paths), and `scanJob` derives it for rows finished before v0.4.

Deviations:

- `internal/metrics`, `internal/webhook` and `internal/api/sync.go` needed no change: the
  metric label is free text, the webhook copies `status`, and the sync wait uses
  `Status.Terminal()`.
- The admin job metadata carries `judge_policy` and `verification`; the admin list
  accepts `status=extracted`.
- The store tests ran on SQLite and on a local throwaway PostgreSQL
  (`VDE_TEST_POSTGRES`).

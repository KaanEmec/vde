# Job: Failure codes after `retention: none`

Version: v1.3 · Epic: 02 Verification and operations · Status: done · Depends on: none

Done 2026-10-22: tests pass on main (orchestrator).

## Goal

Make the error code of a failed job findable by its job id after `retention: none` has
deleted the job, without keeping anything on the job row the contract says must go.

## Context

TravelInbox found an empty `error` on all 12 failed jobs of its comparison after reading
them, and asked whether `error.code` could stay on the job row. Contract §10 says no: with
`retention: none` the service MUST delete "the result, feedback and error" on the first
terminal `GET`. Contract §12 keeps, per job, an audit record with the "terminal status,
attempts, `service_version`, error code". vde already writes it: the `job.finished` audit
event carries `error_code` (`internal/audit/audit.go`), kept for `audit.retention`
(365 days by default). What is missing is a way to find it by job id: `vde audit list`
and `GET /admin/v1/audit` filter by application, time and kind only.

## Tasks

- [x] `vde audit list --job <id>` and a `job_id` query parameter on `GET /admin/v1/audit`
      (additive in `api/admin-openapi.yaml`); both return every event of that job.
- [x] Check that every failure path writes `job.finished` with its code: `timeout`,
      `provider_unavailable`, `extractor_failed`, `judge_failed`, `internal`, and a job
      expired before it ran (`job.expired`); add the missing ones.
- [x] Tests: a job that fails with the fake provider under `retention: none`, read once
      (job gone, `404`), then its code found through `vde audit list --job` and the admin
      endpoint.
- [x] Docs: "Why did a job fail after `retention: none`?" in the
      [API guide](../../../api/guide.md) and [operations](../../../operator/operations.md),
      with the two commands. (The answer to TravelInbox's question 2 in epic 01 job 03's
      hand-over is left to that job.)

## Acceptance criteria

- The tests above pass; the job row keeps no error after its terminal read (contract §10
  unchanged, conformance item 8 passes).
- The admin API snapshot test still passes (the parameter is additive).

## Out of scope

Keeping any field on the job row after `retention: none`; error messages in the audit
record (they may name a provider or limit, but the code is enough to diagnose).

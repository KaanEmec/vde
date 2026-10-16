# Job: Job state machine and leasing

Version: v0.2 · Epic: 03 Job store and worker · Status: done · Depends on: 01

## Goal

The `queued → running → accepted | rejected | failed` transitions enforced in one place,
with lease columns so a crashed worker's job is picked up again without exceeding the
attempt limit or the deadline.

## Context

Contract §5 (state machine, terminal status never changes, `attempts`, `finished_at`,
`expires_at`), §6 (time limit counted from `created_at` including queue time). ADR 0003.

## Tasks

- [x] `job.Status` type with the five values and a `Terminal()` method; `Finish` refuses
      to change a terminal row (returns a conflict error that is logged, never surfaced).
- [x] `LeaseNextQueued` in one `UPDATE … WHERE id = (SELECT … ORDER BY created_at LIMIT
      1) RETURNING *` statement (SQLite supports `RETURNING`), setting `status =
      running`, `started_at`, `lease_until`, `worker_id`. Also re-leases `running` rows
      whose `lease_until` passed.
- [x] `RecordAttempt` persists after every extractor call: `attempts`, last candidate,
      last feedback, and renews the lease. A re-leased job resumes with the persisted
      attempt count and feedback, so the limit is never exceeded across a crash.
- [x] A job whose deadline (`created_at + job_timeout`) has passed when leased is
      finished as `failed/timeout` without a provider call.
- [x] `expires_at` = `created_at + 24h` for `retention: none`, `created_at + application
      retention` otherwise; set at creation.
- [x] Tests: happy path, re-lease after a simulated crash resumes at the right attempt,
      terminal rows are immutable, deadline on lease.

## Acceptance criteria

- A property-style test over random sequences of transitions never observes a terminal
  status changing or `attempts` decreasing.

## Out of scope

Priorities, scheduling fairness between applications beyond the in-progress ceiling.

## Outcome

Done in `internal/job` (state machine in the SQL of `LeaseNextQueued`, `RenewLease`,
`RecordAttempt`, `Finish`, `MarkContentDeleted`) and `internal/worker` (deadline).
Deviations:

- The deadline on lease is enforced **in the worker**, which knows `job_timeout`: a job
  leased at or after `created_at + job_timeout` is finished `failed/timeout` with its
  persisted candidate and feedback, without a provider call.
- `LeaseNextQueued(workerID, now, until, maxInProgress map[app]int)` applies the
  per-application ceiling in the same statement (`json_each` over the map; live leases
  are counted).
- Leases are renewed by a heartbeat (`RenewLease`, every lease/3) besides
  `RecordAttempt`, because one provider call can outlast a short lease.
- `extract.Input` has no field for a prior candidate, so a resumed job continues with
  the remaining attempts but its next extractor request does not carry the previous
  candidate and feedback; the persisted candidate stands until the run produces a new
  one (and turns `extractor_failed` into `rejected` when it exists).
- Property test: 20 seeded random sequences of 60 lease/record/renew/finish/delete
  operations over three jobs.

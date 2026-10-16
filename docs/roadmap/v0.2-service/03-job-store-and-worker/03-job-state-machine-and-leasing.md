# Job: Job state machine and leasing

Version: v0.2 · Epic: 03 Job store and worker · Status: planned · Depends on: 01

## Goal

The `queued → running → accepted | rejected | failed` transitions enforced in one place,
with lease columns so a crashed worker's job is picked up again without exceeding the
attempt limit or the deadline.

## Context

Contract §5 (state machine, terminal status never changes, `attempts`, `finished_at`,
`expires_at`), §6 (time limit counted from `created_at` including queue time). ADR 0003.

## Tasks

- [ ] `job.Status` type with the five values and a `Terminal()` method; `Finish` refuses
      to change a terminal row (returns a conflict error that is logged, never surfaced).
- [ ] `LeaseNextQueued` in one `UPDATE … WHERE id = (SELECT … ORDER BY created_at LIMIT
      1) RETURNING *` statement (SQLite supports `RETURNING`), setting `status =
      running`, `started_at`, `lease_until`, `worker_id`. Also re-leases `running` rows
      whose `lease_until` passed.
- [ ] `RecordAttempt` persists after every extractor call: `attempts`, last candidate,
      last feedback, and renews the lease. A re-leased job resumes with the persisted
      attempt count and feedback, so the limit is never exceeded across a crash.
- [ ] A job whose deadline (`created_at + job_timeout`) has passed when leased is
      finished as `failed/timeout` without a provider call.
- [ ] `expires_at` = `created_at + 24h` for `retention: none`, `created_at + application
      retention` otherwise; set at creation.
- [ ] Tests: happy path, re-lease after a simulated crash resumes at the right attempt,
      terminal rows are immutable, deadline on lease.

## Acceptance criteria

- A property-style test over random sequences of transitions never observes a terminal
  status changing or `attempts` decreasing.

## Out of scope

Priorities, scheduling fairness between applications beyond the in-progress ceiling.

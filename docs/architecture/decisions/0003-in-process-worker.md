# ADR 0003: In-process worker with database leases, no message broker

Status: accepted (planning), 2026-10-16

## Context

Jobs are asynchronous (submit, then poll). The brief keeps the workflow "a small bounded
loop" and makes Temporal optional. Adding a queue system would add a second service to
deploy and operate.

## Decision

- The `serve` command runs the HTTP API, a worker pool and a retention sweeper in one
  process. The worker pool size and the per-application in-progress ceiling are configured.
- The queue is the `jobs` table: a worker leases the oldest `queued` job by setting
  `lease_until` in one transaction, runs it, and writes the terminal state. A job whose
  lease expired without a terminal state is re-leased; the loop is idempotent with respect
  to quota (charged at submit) and the attempt count is persisted per attempt so a restart
  does not exceed the limit.
- Job deadline (default 5 minutes, maximum 8) is enforced with a context deadline computed
  from `created_at`, so queue time counts.
- Graceful shutdown: stop leasing, give running jobs until the deadline or a shutdown grace
  period, then exit; unfinished jobs are picked up again after restart.

## Consequences

- No broker to deploy. Scaling is vertical first; v0.3 adds multi-replica leasing on
  Postgres using the same lease columns.
- Temporal or any other engine remains possible behind the same `JobStore` boundary but is
  not planned.

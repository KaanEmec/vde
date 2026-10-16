# Job: Resource limits and backpressure

Version: v1.0 · Epic: 03 Performance · Status: planned · Depends on: 01

## Goal

The service degrades predictably: a global ceiling on queued jobs, bounded memory for
uploads, and `503 unavailable` with `Retry-After` when saturated instead of slow failure.

## Tasks

- [ ] `limits.max_queued_jobs` (global) → `503 unavailable` at submit when exceeded.
- [ ] Stream uploads to blob storage instead of buffering whole parts in memory where
      possible (document part up to 25 MiB), keeping the "read whole request before
      answering" rule.
- [ ] Soft memory limit via `GOMEMLIMIT` guidance in docs; tune based on the load test.

## Acceptance criteria

- Under a flood of submissions the service answers `503` quickly and keeps polls fast.

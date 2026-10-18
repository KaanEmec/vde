# Epic 07: Horizontal scaling

Version: v0.3 · Status: done· Depends on: 01 (Postgres, S3)

Run several `vde` processes against shared storage without changing the single-process
code path: API-only and worker-only modes, shared leasing, and documented limits of
per-process state.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Process roles and shared leasing](01-process-roles-and-leasing.md) | done |
| 02 | [Shared rate limiting and health state](02-shared-limits.md) | done |

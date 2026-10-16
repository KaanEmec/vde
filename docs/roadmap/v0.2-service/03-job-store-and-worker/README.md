# Epic 03: Job store and worker

Version: v0.2 · Status: done · Depends on: v0.1

Persistent jobs in SQLite, blobs on disk, a state machine with leases, and the in-process
worker pool that runs the v0.1 loop under the job deadline.

| # | Job | Status |
| --- | --- | --- |
| 01 | [SQLite store and migrations](01-sqlite-store-and-migrations.md) | done |
| 02 | [Filesystem blob store](02-blob-store.md) | done |
| 03 | [Job state machine and leasing](03-job-state-machine-and-leasing.md) | done |
| 04 | [Worker pool and job deadline](04-worker-pool.md) | done |

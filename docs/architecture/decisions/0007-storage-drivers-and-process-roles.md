# ADR 0007: Storage drivers and process roles, no broker, no Redis

Status: accepted, 2026-10-17

## Context

ADR 0002 chose SQLite and local files and ADR 0003 an in-process worker with database
leases, both with v0.3 multi-replica operation in mind. v0.3 delivered it. Deployments that
outgrow one VM, or that run on platforms without a persistent local disk, need shared
storage and a way to run the API and the workers as separate processes.

## Decision

- **Drivers are configuration, not build variants.** `storage.driver` selects `sqlite`
  (default) or `postgres`; `blobs.driver` selects `fs` (default) or `s3` (any S3-compatible
  store). One binary contains all of them, pure Go. Both stores keep the small interfaces
  of ADR 0002; Postgres shares one `database/sql` implementation with SQLite (statements
  written once, placeholders rewritten), and only leasing (`FOR UPDATE SKIP LOCKED`) and
  one JSON statement differ. Migrations exist per dialect and are applied by `vde serve` at
  start or explicitly by `vde migrate`.
- **Process roles.** `vde serve --role all|api|worker` (default `all`, the unchanged
  single-process path). `api` serves `/v1` and the admin API; `worker` runs the pool and the
  sweeper and serves only `/healthz` and `/metrics`. Workers coordinate through the job
  table's leases (renewed during an attempt, expiring after `worker.lease`, default 2 min),
  so a killed worker's job resumes on another. Wake-up across processes is polling
  (`worker.poll_interval`); `LISTEN/NOTIFY` was skipped for its connection handling cost
  against at most half a second of latency. Worker ids include host, pid and a random part,
  since containers share pid 1.
- **One sweeper at a time.** The sweeper takes a Postgres session advisory lock per sweep
  and skips the sweep when another process holds it.
- **No message broker.** The jobs table remains the queue (ADR 0003).
- **No Redis.** Quota is already exact because it lives in the store. Per-process rate
  limit buckets stay the default (limits multiply by replica count, documented); where
  exactness matters, `rate.shared: true` keeps fixed one-minute counters in the store
  (`rate_windows`, one capped upsert per request). A cache service for this is not
  justified. Endpoint health stays per process and self-healing.

## Consequences

- SQLite deployments are unchanged; nothing new to run.
- Scaling out needs Postgres and an S3-compatible bucket, and nothing else; the scaled
  compose example shows one API and two workers.
- `max_in_progress` is not locked across replicas and may be exceeded by the number of
  replicas leasing at the same instant. Pending webhook deliveries are in memory and are
  lost on restart; callers keep polling as the fallback.
- The fixed-window rate counters allow a boundary burst of up to twice the limit.
- Revisit Redis or a broker only with measured evidence that the store is the bottleneck.

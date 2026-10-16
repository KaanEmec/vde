# Job: Load test and capacity document

Version: v1.0 · Epic: 03 Performance · Status: planned · Depends on: v0.2

## Goal

Know and publish how many jobs per minute one process handles on a small VM with the
fake provider (service overhead) and with a real provider (end-to-end), and where the
limits are.

## Tasks

- [ ] A `k6` or Go-based load script submitting realistic multipart bodies and polling;
      measure submit latency, poll latency, worker throughput, SQLite contention, memory
      with 25 MiB uploads in flight.
- [ ] `docs/operator/capacity.md`: numbers, recommended worker counts per CPU, when to
      move to Postgres and multiple replicas.

## Acceptance criteria

- Baseline recorded for one 2-vCPU VM; no unbounded memory growth over a one-hour run.

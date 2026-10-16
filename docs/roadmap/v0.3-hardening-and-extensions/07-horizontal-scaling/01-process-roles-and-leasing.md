# Job: Process roles and shared leasing

Version: v0.3 · Epic: 07 Horizontal scaling · Status: planned · Depends on: 01-01, 01-02

## Goal

`vde serve --role api|worker|all` (default `all`) so deployments can scale the API and
the workers separately on Postgres and S3, with leases preventing double processing.

## Tasks

- [ ] Role flag gates the worker pool and sweeper; the sweeper runs on workers only,
      with a database advisory lock so one sweeps at a time.
- [ ] Lease renewal during long attempts; lease expiry tuned (default 2 min) with
      documented reasoning; the wake-up channel becomes a short poll interval across
      processes (or Postgres `LISTEN/NOTIFY`, if cheap to add).
- [ ] `/healthz` on an API-only process does not require worker health but reports
      queue age (oldest `queued` job) so an orchestrator can detect missing workers.
- [ ] Compose example with two workers and one API on Postgres and MinIO; a test runs
      two worker processes against one database and asserts no job is processed twice.

## Acceptance criteria

- 100 jobs with two workers: each processed exactly once; killing a worker mid-job
  results in resumption on the other within the lease expiry.

## Out of scope

Autoscaling hooks, Kubernetes manifests (backlog Helm chart).

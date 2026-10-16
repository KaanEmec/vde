# Job: Process roles and shared leasing

Version: v0.3 · Epic: 07 Horizontal scaling · Status: done · Depends on: 01-01, 01-02

## Goal

`vde serve --role api|worker|all` (default `all`) so deployments can scale the API and
the workers separately on Postgres and S3, with leases preventing double processing.

## Tasks

- [x] Role flag gates the worker pool and sweeper; the sweeper runs on workers only,
      with a database advisory lock so one sweeps at a time.
- [x] Lease renewal during long attempts; lease expiry tuned (default 2 min) with
      documented reasoning; the wake-up channel becomes a short poll interval across
      processes (or Postgres `LISTEN/NOTIFY`, if cheap to add).
- [x] `/healthz` on an API-only process does not require worker health but reports
      queue age (oldest `queued` job) so an orchestrator can detect missing workers.
- [x] Compose example with two workers and one API on Postgres and MinIO; a test runs
      two worker processes against one database and asserts no job is processed twice.

## Acceptance criteria

- 100 jobs with two workers: each processed exactly once; killing a worker mid-job
  results in resumption on the other within the lease expiry.

## Out of scope

Autoscaling hooks, Kubernetes manifests (backlog Helm chart).

## Outcome

- `vde serve --role all|api|worker` (`cmd/vde/serve.go`, `serveAs`; anything else is a
  usage error). `api`: the HTTP API and admin API, no pool, no sweeper, no in-process wake
  (`POST /v1/extract` polls the store). `worker`: pool and sweeper; `server.listen` serves
  only `/healthz` and `/metrics` (`api.Deps.Role`), `admin.listen` is not opened. `all` is
  unchanged.
- Leases: `worker.lease` (default 2 min, at least 3 s; renewed every lease/3 and at each
  attempt, as before). Reasoning in the configuration reference: two missed renewals are
  survived, and a dead worker's job resumes well inside the 5-minute job timeout. Worker
  ids are `<host>-<pid>-<random>`: in containers every process is pid 1, and the old
  `vde-<pid>` would have let two replicas renew or finish each other's jobs.
- Wake-up across processes is the existing poll (`worker.poll_interval`, 500 ms); the
  in-process channel stays. `LISTEN/NOTIFY` was skipped: with `database/sql` it needs a
  dedicated pgx connection, reconnection and a SQLite twin, more than the ~40 lines it
  was allowed, for at most half a second of latency.
- Sweeper: `sweeper.Options.Lock` (`sweeper.Locker`); `Run` takes it without waiting per
  sweep and skips the sweep when another process holds it. `job.Postgres.TryLock` is a
  session-level `pg_try_advisory_lock` on a dedicated connection (released with
  `pg_advisory_unlock`, the connection discarded if that fails; a dead process's session
  frees it); `job.SQLite.TryLock` always grants. Key `job.SweepLock`.
- `/healthz` on `--role api`: no role groups; `queue_age_seconds` from the new
  `Store.OldestQueuedAt` (one `MIN` over the `jobs_queue` index); it never makes the answer
  `503`. The other roles check every group of a role (escalation, second judge, feedback
  group), not only the primary groups.
- `examples/compose/scaled/`: PostgreSQL 17, RustFS 1.0.1 (MinIO's image is not pullable),
  a bucket-creating `aws-cli` one-shot, a `migrate` one-shot, one `api` and a `worker`
  service with two replicas, `rate.shared: true`. Run locally with a locally built image:
  all three vde processes healthy, jobs submitted to the API ran on the workers.
- Tests: `TestTwoPoolsProcessEachJobOnce` (two pools, each with its own connection pool, on
  one PostgreSQL schema: 100 jobs, each with exactly one extractor and one judge call and
  one terminal write, both pools take part) and `TestKilledWorkerResumesElsewhere` (2 s
  lease: the killed pool's job finishes on the other within the lease plus 1 s), both
  skipped without `VDE_TEST_POSTGRES`; `TestTryLockPostgres` (refused while held, free after
  release and after the holder's backend is terminated), `TestRunSkipsWithoutTheLock`,
  `TestOldestQueuedAt`, `TestHealthzAPIRole`, `TestWorkerRoleRoutes`, `TestServeRoles`,
  `TestHealthCoversEveryGroup`, `TestEscalationGroupCapabilities`.

Also done here (follow-ups of epic 03): submit and the worker select the representation
with `extract.RoleCapabilities(cfg)`; provider-call metrics carry a `group` label
(`router.Group.OnCall` replaces the per-endpoint metering wrapper, so every group, the
second judge included, is labelled); `vde config check` prints escalation steps, the
second judge and the classifier feedback fallback, plus the worker and rate settings.

Deviations:

- The two "processes" of the acceptance test are two worker pools with separate
  connection pools in one test binary; killing one is cancelling it without a finish, which
  is what a crash leaves in the database (the lease and the persisted attempts).
- A worker process still serves `server.listen`, for `/healthz` and `/metrics`; it needs
  the same listen rules (TLS, `trusted_proxy` or `insecure_http`) as the API.

# Job: `GET /healthz`

Version: v0.2 · Epic: 07 Operations · Status: done · Depends on: 01-01, 03-01, v0.1 03-03

## Goal

An unauthenticated liveness and readiness endpoint that says whether the service can
accept jobs.

## Context

Contract §12: `200` without authentication when the database and queue are reachable and
at least one endpoint per role is configured and not marked down, else `503`; body
`{"status": "ok"}`.

## Tasks

- [x] Handler at the root (not under `/v1`): ping the database (`SELECT 1` with a 1 s
      timeout), check `Router.Health()` for both roles, check the data directory is
      writable (a cached check every 30 s); all good → `200 {"status":"ok"}`; else `503
      {"status":"unavailable","reasons":[…]}` with reason codes only (`db`, `blobs`,
      `role_extractor_down`), no details.
- [x] Rate-limit the endpoint lightly by IP (it is unauthenticated) or document that it
      should be exposed only to the orchestrator; choose: no auth, cheap checks, cached.
- [x] Docker `HEALTHCHECK` uses it (epic 08, via `vde healthcheck`).

## Acceptance criteria

- With the fake provider marked down for the judge role, `/healthz` returns `503` with
  `role_judge_down`; recovery returns `200`.

## Out of scope

Separate liveness and readiness endpoints (one suffices for one process).

## Notes and deviations

- `internal/api/health.go`. The database check is `CountRunning` on the jobs table (the
  queue) with a 1 s timeout, through `job.Store` (no ping method on the interface). The
  data directory probe creates and removes a temporary file in `server.data_dir`,
  cached 30 s; failure reports `blobs`. Reasons: `db`, `blobs`, `role_extractor_down`,
  `role_judge_down`; the 503 body also carries the contract §9 error object (no details).
- Choice for the unauthenticated endpoint: no rate limit; cheap and cached checks;
  `docs/operator/configuration.md` says to expose it to the orchestrator only.
- Tests: `internal/api/health_test.go` (each reason, judge down → `503
  role_judge_down` → recovery `200`, cached data directory check with a fake clock).

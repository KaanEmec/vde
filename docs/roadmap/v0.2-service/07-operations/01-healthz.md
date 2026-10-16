# Job: `GET /healthz`

Version: v0.2 · Epic: 07 Operations · Status: planned · Depends on: 01-01, 03-01, v0.1 03-03

## Goal

An unauthenticated liveness and readiness endpoint that says whether the service can
accept jobs.

## Context

Contract §12: `200` without authentication when the database and queue are reachable and
at least one endpoint per role is configured and not marked down, else `503`; body
`{"status": "ok"}`.

## Tasks

- [ ] Handler at the root (not under `/v1`): ping the database (`SELECT 1` with a 1 s
      timeout), check `Router.Health()` for both roles, check the data directory is
      writable (a cached check every 30 s); all good → `200 {"status":"ok"}`; else `503
      {"status":"unavailable","reasons":[…]}` with reason codes only (`db`, `blobs`,
      `role_extractor_down`), no details.
- [ ] Rate-limit the endpoint lightly by IP (it is unauthenticated) or document that it
      should be exposed only to the orchestrator; choose: no auth, cheap checks, cached.
- [ ] Docker `HEALTHCHECK` uses it (epic 08).

## Acceptance criteria

- With the fake provider marked down for the judge role, `/healthz` returns `503` with
  `role_judge_down`; recovery returns `200`.

## Out of scope

Separate liveness and readiness endpoints (one suffices for one process).

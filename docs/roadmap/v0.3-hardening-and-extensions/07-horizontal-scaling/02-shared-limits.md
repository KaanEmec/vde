# Job: Shared rate limiting and health state

Version: v0.3 · Epic: 07 Horizontal scaling · Status: in progress · Depends on: 01

## Goal

Make the per-process rate limits and endpoint health acceptable or shared in multi-replica
deployments, choosing the simplest adequate approach.

## Tasks

- [ ] Rate limits: document that per-process token buckets multiply by replica count and
      offer `rate.shared: true` using Postgres-backed counters (fixed-window counters per
      minute, one `UPSERT` per request) when exactness matters. Quota is already exact
      (store-backed).
- [ ] Endpoint health: keep per-process (cheap, self-healing); expose per-process state
      in metrics with an instance label.
- [ ] No Redis: adding a cache service for this is not justified; revisit only with
      evidence.

## Acceptance criteria

- With `rate.shared: true`, two API replicas enforce one combined limit in a test.

## Out of scope

Distributed circuit breakers.

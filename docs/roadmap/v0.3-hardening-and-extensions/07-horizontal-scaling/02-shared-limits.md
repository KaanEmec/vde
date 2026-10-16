# Job: Shared rate limiting and health state

Version: v0.3 · Epic: 07 Horizontal scaling · Status: done · Depends on: 01

## Goal

Make the per-process rate limits and endpoint health acceptable or shared in multi-replica
deployments, choosing the simplest adequate approach.

## Tasks

- [x] Rate limits: document that per-process token buckets multiply by replica count and
      offer `rate.shared: true` using Postgres-backed counters (fixed-window counters per
      minute, one `UPSERT` per request) when exactness matters. Quota is already exact
      (store-backed).
- [x] Endpoint health: keep per-process (cheap, self-healing); expose per-process state
      in metrics with an instance label.
- [x] No Redis: adding a cache service for this is not justified; revisit only with
      evidence.

## Acceptance criteria

- With `rate.shared: true`, two API replicas enforce one combined limit in a test.

## Out of scope

Distributed circuit breakers.

## Outcome

- Top-level `rate.shared` (default `false`; a warning with `storage.driver: sqlite`).
  When on, `requests_per_minute` and `submits_per_minute` go through
  `Store.IncrementRate`: one capped `INSERT … ON CONFLICT DO UPDATE … WHERE count < limit
  RETURNING count` on the new `rate_windows` table (application, kind, UTC minute),
  migration `0007_rate_windows` on both dialects. A refusal is `429 rate_limited` with
  `Retry-After` until the next minute; a store failure admits the request (logged), the
  quota still holding. The admin API's limiter stays in memory. The sweeper deletes
  windows older than two minutes (`Result.RateRows`).
- Per-process buckets stay the default; the replica multiplication and the fixed window's
  boundary burst are documented in storage and the configuration reference.
- Endpoint health stays per process; `vde_endpoint_up{instance,group,endpoint,model}` is
  read from every group's pair state at scrape (`metrics.WatchEndpoints`), on processes
  that run workers. `instance` is the process's id, the one in its worker ids.
- Tests: `TestSharedRateLimit` (two handlers with their own in-memory limiters on one
  store: 5 of 20 requests admitted with `shared`, 10 without) and
  `TestSharedRateLimitPostgres` (the same on PostgreSQL), `TestIncrementRate` on both
  drivers, metrics series in `TestSeries`.

Deviations:

- `rate.shared` is a top-level key (one choice for the deployment), not per application.
- Prometheus attaches its own `instance` target label; the series' `instance` then shows as
  `exported_instance` unless the scrape sets `honor_labels`. The name was kept as the job
  asked; it identifies the process even when several replicas sit behind one scrape
  address.

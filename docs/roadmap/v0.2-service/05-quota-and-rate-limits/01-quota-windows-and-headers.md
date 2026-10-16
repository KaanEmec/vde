# Job: Quota windows and headers

Version: v0.2 · Epic: 05 Quota and rate limits · Status: planned · Depends on: 02-01, 03-01

## Goal

Count submissions per application per window, refuse with `429 quota_exhausted` when
spent, and put `X-Quota-Remaining` and `X-Quota-Reset` on every authenticated response.

## Context

Contract §8: submissions count, polls and quota reads do not; `429` with
`X-Quota-Remaining: 0` when spent; both headers on every authenticated response including
errors; every `429` carries `Retry-After`. Contract §14 item 7.

## Tasks

- [ ] Default quota 1000 per `P1D` (decision 16). Window arithmetic: `window` as ISO 8601 duration (`P1D`, `PT1H`, `P7D`), aligned
      to UTC boundaries (a day window resets at 00:00 UTC) so `X-Quota-Reset` is
      predictable; `quota_usage(application_id, window_start, count)` row per window.
- [ ] Charge in the submit transaction: `INSERT … ON CONFLICT DO UPDATE SET count =
      count + 1 WHERE count < limit RETURNING count`; no row returned → `429
      quota_exhausted`, `Retry-After` = seconds to reset. Idempotent replays do not
      charge. Quota is not refunded on job failure (document; keep it simple).
- [ ] Middleware after auth computes remaining and reset for the application and sets
      the two headers on the response writer before the handler runs (so errors carry
      them); the submit handler updates `X-Quota-Remaining` after charging.
- [ ] Old `quota_usage` rows removed by the sweeper (epic 06).
- [ ] Tests: headers on 200, 401 is exempt (unauthenticated), 404, 429; exhaustion at
      exactly `limit`; reset boundary; replay does not charge.

## Acceptance criteria

- Contract §14 item 7 passes: spent quota → 429 with the three headers, polls still work.

## Out of scope

Refunds, soft limits, per-token quotas.

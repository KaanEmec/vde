# Job: Rate limits and in-progress ceiling

Version: v0.2 · Epic: 05 Quota and rate limits · Status: planned · Depends on: 01

## Goal

Per-application ceilings on requests per minute, submissions per minute and jobs in
progress, answering `429 rate_limited` with `Retry-After` and quota headers above zero.

## Context

Contract §8 (rate limit separate from quota, SHOULD include jobs in progress; polls every
2 s backing off to 30 s need a generous limit), §5 (polling SHOULD have a generous rate
limit). Decision 16: defaults 300 requests/min, 30 submissions/min, 20 in progress.

## Tasks

- [ ] In-memory token buckets per application keyed by application id: `requests_per_
      minute` (default 300) applied after auth to every `/v1` request;
      `submits_per_minute` (default 30) applied to `POST /v1/jobs` before quota charge.
      In-memory is fine for one process (ADR 0003); v0.3 multi-replica accepts
      per-replica limits and documents it.
- [ ] `max_in_progress` (default 20): counted from the store (`queued` + `running` per
      application) at submit; over the ceiling → `429 rate_limited` with `Retry-After:
      5`.
- [ ] `Retry-After` from the bucket's refill time, rounded up to whole seconds, minimum 1.
- [ ] Tests: burst over the request limit → 429 then recovery; submit limit independent
      of request limit; in-progress ceiling with jobs held `running` by a blocked fake
      provider.

## Acceptance criteria

- A poll loop at 2 s per job for 20 jobs in flight (600 requests per minute at the
  fastest cadence, backing off to 40) stays under the default request limit once
  TravelInbox's backoff kicks in; the first 30 seconds of a burst are covered by the
  bucket size (one minute of allowance). Document this interaction and the knobs.

## Out of scope

Global (cross-application) limits beyond worker pool size, IP-based limiting (reverse
proxy concern).

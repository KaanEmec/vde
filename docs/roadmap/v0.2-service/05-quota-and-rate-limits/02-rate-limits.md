# Job: Rate limits and in-progress ceiling

Version: v0.2 · Epic: 05 Quota and rate limits · Status: done · Depends on: 01

## Goal

Per-application ceilings on requests per minute, submissions per minute and jobs in
progress, answering `429 rate_limited` with `Retry-After` and quota headers above zero.

## Context

Contract §8 (rate limit separate from quota, SHOULD include jobs in progress; polls every
2 s backing off to 30 s need a generous limit), §5 (polling SHOULD have a generous rate
limit). Decision 16: defaults 300 requests/min, 30 submissions/min, 20 in progress.

## Tasks

- [x] In-memory token buckets per application keyed by application id: `requests_per_
      minute` (default 300) applied after auth to every `/v1` request;
      `submits_per_minute` (default 30) applied to `POST /v1/jobs` before quota charge.
      In-memory is fine for one process (ADR 0003); v0.3 multi-replica accepts
      per-replica limits and documents it.
- [x] `max_in_progress` (default 20): counted from the store (`queued` + `running` per
      application) at submit; over the ceiling → `429 rate_limited` with `Retry-After:
      5`.
- [x] `Retry-After` from the bucket's refill time, rounded up to whole seconds, minimum 1.
- [x] Tests: burst over the request limit → 429 then recovery; submit limit independent
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

## Notes

Built in `internal/api/ratelimit.go` (token buckets, per-key idempotency locks),
the `limit` step in `middleware.go` (requests per minute) and `admit` in `submit.go`
(submits per minute, in-progress ceiling, quota); tests in `ratelimit_test.go`.

- Buckets hold one minute of allowance and refill continuously; `Retry-After` is the
  time to the next token rounded up to whole seconds, at least 1. A refused request
  takes no token. Invalid submissions are refused before the submit bucket, so they
  do not use submit tokens (they do use request tokens).
- `max_in_progress` is counted with `Store.CountRunning`, which counts `running` jobs
  only: the store has no `queued` + `running` count and this job does not change
  `internal/job`. Queued jobs wait anyway, because the worker pool leases no more than
  `max_in_progress` jobs per application. A `CountInProgress` store method would make
  the ceiling also bound the queue (follow-up).
- The limiter lives in a package-level map keyed by a weak pointer to the server (the
  `server` struct in `api.go` was not changed); it can move into a `server` field.
- A rate-limited request that announced a body is answered with `Connection: close`
  instead of draining it.

### Polling and the request limit

Every `/v1` request takes a request token: submissions, polls, quota reads. TravelInbox
polls each job every 2 s, backing off to every 30 s. With 20 jobs in flight at the fastest
cadence that is 600 requests per minute against the default 300: the bucket starts
full (300 tokens, one minute of allowance) and refills at 5 per second while the polls
take 10, so it covers about 60 seconds of the fastest cadence before polls see `429`
with a `Retry-After` of 1 second. Once the backoff reaches 30 s the same 20 jobs need 40
requests per minute. A client that does not back off, or more jobs in flight (raise
`max_in_progress`), needs a higher `rate.requests_per_minute`; `rate.submits_per_minute`
(default 30) and `rate.max_in_progress` (default 20) are separate knobs and do not
consume the request allowance beyond the one token each request takes.

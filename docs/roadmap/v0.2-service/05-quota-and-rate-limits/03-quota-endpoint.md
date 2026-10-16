# Job: `GET /v1/quota`

Version: v0.2 · Epic: 05 Quota and rate limits · Status: done · Depends on: 01

## Goal

Return the application's quota numbers as a body without changing them, so a client can
seed its gauge and verify its credential at start.

## Context

Contract §8: `{"limit", "remaining", "reset", "window"}`, same numbers as the headers,
does not change them. TravelInbox calls it at worker start.

## Tasks

- [x] Handler reads the current window row (or computes `remaining = limit` when none)
      and returns the body plus the two headers.
- [x] `window` echoed as configured (ISO 8601 string).
- [x] Test: body equals headers; calling it 100 times changes nothing; counts against
      the request rate limit but not the quota.

## Acceptance criteria

- Matches the contract example shape.

## Out of scope

Historical usage, per-token breakdown.

## Notes and deviations

- `internal/api/quota_get.go` reads the window through `currentQuota` and sets the
  headers with `setQuotaHeaders` (both in `quota.go`, epic 05-01), from the same read as
  the body, so body and headers always agree.
- Tests: `internal/api/quota_get_test.go` (body equals headers; 100 reads change
  nothing and the window count stays put; 401 without a token); the rate limit counting
  quota reads is 05-02's test. `cmd/vde/serve_e2e_test.go` checks the numbers end to
  end, before and after a submission.

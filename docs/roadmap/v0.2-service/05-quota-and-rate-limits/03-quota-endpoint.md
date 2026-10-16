# Job: `GET /v1/quota`

Version: v0.2 · Epic: 05 Quota and rate limits · Status: planned · Depends on: 01

## Goal

Return the application's quota numbers as a body without changing them, so a client can
seed its gauge and verify its credential at start.

## Context

Contract §8: `{"limit", "remaining", "reset", "window"}`, same numbers as the headers,
does not change them. TravelInbox calls it at worker start.

## Tasks

- [ ] Handler reads the current window row (or computes `remaining = limit` when none)
      and returns the body plus the two headers.
- [ ] `window` echoed as configured (ISO 8601 string).
- [ ] Test: body equals headers; calling it 100 times changes nothing; counts against
      the request rate limit but not the quota.

## Acceptance criteria

- Matches the contract example shape.

## Out of scope

Historical usage, per-token breakdown.

# Job: Failover bounds and error classification

Version: v0.1 · Epic: 03 Endpoint groups and failover · Status: planned · Depends on: 01

## Goal

Failover can never loop or exceed the job deadline, and every error that reaches the loop
carries enough classification for the loop to pick the right job error code.

## Context

Brief: "Bound retries and timeouts so endpoint failover cannot create an endless loop."
Contract §9 job error codes `provider_unavailable`, `extractor_failed`, `judge_failed`,
`timeout`.

## Tasks

- [ ] Group config: `max_tries` (default: number of pairs), `max_total_time` (default 3m,
      must be < job timeout; `config check` enforces), `retry_same_endpoint_once` for 429
      with a short `Retry-After` (default false; honoured only when `Retry-After` ≤ 10 s).
- [ ] Backoff between tries: fixed short wait (250 ms) with jitter; no exponential
      backoff inside a job, the job deadline is the real bound.
- [ ] Error taxonomy returned by the router: `ErrGroupUnavailable` (all retryable
      failures), `ErrPermanent` wrapping provider error (refusal, bad request), and
      context errors propagated unchanged so the loop maps deadline to `timeout`.
- [ ] A test that a group with slow endpoints respects `max_total_time` and a test that
      the job deadline cuts a call short.

## Acceptance criteria

- No code path in the router can call a provider after the context is done.
- Error mapping table documented in `internal/router/doc.go` and exercised by tests.

## Out of scope

Circuit breaking across jobs (next job).

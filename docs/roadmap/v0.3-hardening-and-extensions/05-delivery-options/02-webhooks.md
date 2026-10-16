# Job: Completion webhooks

Version: v0.3 · Epic: 05 Delivery options · Status: planned · Depends on: v0.2 03-04

## Goal

Optionally notify a caller-registered URL when a job reaches a terminal state, signed so
the receiver can verify it, without becoming a second channel for content.

## Context

Contract §16 (MAY; TravelInbox prefers polling: no inbound endpoint, no second secret).

## Tasks

- [ ] Per-application configuration (not per request, to keep requests from naming
      arbitrary URLs): `webhook: {url, secret_env, events: [job.finished]}`; the
      operator approves the URL.
- [ ] Payload: job id, status, `service_version`, `finished_at`; never `result` or
      `feedback` (the caller fetches the job, which keeps retention semantics intact).
- [ ] HMAC-SHA256 signature header over timestamp and body; retries with backoff for
      a bounded time; delivery attempts in the audit log.
- [ ] Tests with an `httptest` receiver.

## Acceptance criteria

- A finished job triggers one signed delivery; the receiver verifies the signature.

## Out of scope

Per-request callback URLs, payloads with content.

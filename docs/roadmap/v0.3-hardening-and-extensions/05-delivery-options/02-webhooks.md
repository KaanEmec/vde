# Job: Completion webhooks

Version: v0.3 · Epic: 05 Delivery options · Status: done · Depends on: v0.2 03-04

## Goal

Optionally notify a caller-registered URL when a job reaches a terminal state, signed so
the receiver can verify it, without becoming a second channel for content.

## Context

Contract §16 (MAY; TravelInbox prefers polling: no inbound endpoint, no second secret).

## Tasks

- [x] Per-application configuration (not per request, to keep requests from naming
      arbitrary URLs): `webhook: {url, secret_env, events: [job.finished]}`; the
      operator approves the URL.
- [x] Payload: job id, status, `service_version`, `finished_at`; never `result` or
      `feedback` (the caller fetches the job, which keeps retention semantics intact).
- [x] HMAC-SHA256 signature header over timestamp and body; retries with backoff for
      a bounded time; delivery attempts in the audit log.
- [x] Tests with an `httptest` receiver.

## Outcome

`applications[].webhook: {url, secret_env, events}` (`internal/config/delivery.go`): the
URL must be `https` unless loopback or `server.insecure_http`, the secret comes from the
named variable, `events` defaults to `[job.finished]`. `internal/webhook` sends
`{event, job_id, application_id, status, service_version, finished_at}` with
`X-VDE-Event` and `X-VDE-Signature: t=<unix>,v1=<hex HMAC-SHA256 of "<t>.<body>">`; it
ships `Sign` and `Verify` (timestamp tolerance for replay protection). Retries: five
attempts, waits 30 s, 2 m, 5 m, 8 m (about 15 minutes), redirects not followed. Each
attempt is audited (`webhook.delivered`, `webhook.failed` with a fixed reason and
`final`). The pool's `OnFinish` hook queues it; `vde serve` wires and stops it.
Tests: `internal/webhook/webhook_test.go` (one signed delivery verified by the receiver,
retries, give-up, stop, signature checks) and `cmd/vde/webhook_e2e_test.go`.

Deviations:

- One goroutine per pending delivery (sleeping between attempts), capped at 1000 pending;
  beyond that a delivery is dropped and audited `queue_full`. Pending deliveries live in
  memory and are lost on restart, documented with polling as the fallback.
- Applications created over the admin API have no webhook: the URL is operator-approved
  configuration only.

## Acceptance criteria

- A finished job triggers one signed delivery; the receiver verifies the signature.

## Out of scope

Per-request callback URLs, payloads with content.

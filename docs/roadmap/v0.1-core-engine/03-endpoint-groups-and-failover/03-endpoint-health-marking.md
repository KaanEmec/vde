# Job: Endpoint health marking

Version: v0.1 · Epic: 03 Endpoint groups and failover · Status: planned · Depends on: 01, 02

## Goal

An endpoint that just failed is deprioritized for a short time so every job does not pay
its timeout, while remaining a fallback if nothing else works. `/healthz` (v0.2) can report
roles with no healthy endpoint.

## Context

Contract §12: `/healthz` is 200 when "at least one endpoint per role configured and not
marked down".

## Tasks

- [ ] Keep it simple: per endpoint-model pair, a `downUntil` timestamp set after a
      retryable failure (default 30 s, doubling to a cap of 5 m on consecutive failures,
      reset on success). The router orders pairs: healthy first in configured order, then
      marked-down pairs in configured order. Marked-down pairs are still tried if the
      healthy ones fail.
- [ ] Expose `Router.Health()` returning, per role, whether a healthy pair exists, and per
      pair its state (for `/healthz` and `/metrics` in v0.2).
- [ ] In-memory only; no persistence, no distributed state (v0.3 multi-replica keeps it
      per process by design).
- [ ] Tests: a failure marks down; the next call prefers the other pair; after the window
      the order is restored.

## Acceptance criteria

- With the primary endpoint down, a sequence of jobs incurs the primary's timeout once per
  window, not once per job.

## Out of scope

Active health probing, shared health state between replicas.

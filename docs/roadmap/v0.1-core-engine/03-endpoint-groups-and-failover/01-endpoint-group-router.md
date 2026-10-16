# Job: Endpoint group router

Version: v0.1 · Epic: 03 Endpoint groups and failover · Status: planned · Depends on: 02-01

## Goal

`internal/router`: given a role and a request, pick the first eligible endpoint-model pair
of the role's group, call it, and on a retryable error move to the next, within bounds.

## Context

Brief, "Endpoint groups". Contract §6: "Endpoint failover handles availability
(connection errors, 5xx, provider rate limits, timeouts per call), is bounded in tries and
total time, and never crosses into the other role's group."

## Tasks

- [ ] `Group` holds the ordered pairs built from config, each with its `Provider`
      instance, model name and `accepts` set. `Router.Complete(ctx, role, req, mediaTypes)`
      filters pairs by the media types present in the request (a pair that cannot read a
      PDF part is skipped, not tried), then iterates.
- [ ] Each attempt gets a child context with the endpoint's timeout; the whole call gets a
      context bounded by the group's `max_total_time` and the caller's deadline (the job
      deadline), whichever is earlier.
- [ ] Return the response and which pair served it (for metrics and `service_version`
      debugging; never exposed to callers).
- [ ] If every eligible pair fails retryably, or tries are exhausted, return a
      `router.ErrGroupUnavailable` wrapping the last error. A permanent error from a pair
      is returned at once without trying the next pair (it would fail the same way, and
      a refusal must not be retried on another provider by default; make this a group
      option `failover_on_permanent: false`).
- [ ] Table-driven tests with the fake provider: first succeeds; first fails retryable
      then second succeeds; all fail; permanent stops; a pair skipped for media type.

## Acceptance criteria

- Tests pass; the router has no knowledge of roles beyond the group it was given.

## Out of scope

Load balancing or weighted selection (ordered failover only), escalation (v0.3).

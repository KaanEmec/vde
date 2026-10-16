# Job: Contract review and feedback

Version: v0.2 · Epic: 09 Contract conformance · Status: planned · Depends on: 01, 02

## Goal

Close the loop with TravelInbox: answer the contract's open points, list every superset
addition and any deviation, and propose the next contract version so their pinned copy
matches the service's API document.

## Context

Contract §16: open points (webhook and sync endpoint, HTML representation, finer scopes,
`DELETE`, extra judge keys, extra fields) and the change process (once the service
publishes its own API document, that becomes the source and TravelInbox's file pins the
version).

## Tasks

- [ ] Write `docs/conformance/travelinbox-v1-review.md`: decisions on each open point
      (from `docs/roadmap/open-questions.md` 13–16), the superset list (optional schema,
      `DELETE`, `X-Request-Id`, `/v1/openapi.yaml`, `jobs:read` scope, `judge_confidence`,
      `attachments`, `message/rfc822`, raw HTML default, no server-side rendering),
      default limits (300/30/20, quota 1000),
      and anything the implementation found ambiguous in the contract (for example
      whether `attempts` counts an unparseable extractor answer, which the roadmap says
      yes).
- [ ] Propose the pinned version string and link to `api/openapi.yaml` at the release tag.
- [ ] Review meeting notes and resulting changes recorded as an ADR if architectural.

## Acceptance criteria

- TravelInbox has what it needs to update `shared/extraction-service-api.yaml` and its
  stub without reading the service's code.

## Out of scope

Changing `/v1` semantics (would be `/v2`).

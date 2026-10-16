# Job: TravelInbox live test and evaluation run

Version: v0.2 · Epic: 09 Contract conformance · Status: planned · Depends on: 01, 08-02

## Goal

Run TravelInbox's own live test and evaluation against a deployment of the release
candidate with a real provider, and record the results.

## Context

Contract §14: `go test ./internal/extract -run TestServiceLive` and `make eval
EXTRACTOR=service` from TravelInbox's `backend/` with `EXTRACTION_URL` and
`EXTRACTION_TOKEN`; item 10 ("reports a no-correction share for flights and lodging,
whatever the number"). Decision 11 (corpus and stub are co-owned and imported).

## Tasks

- [ ] Deploy the release candidate with the compose example and a real provider key on a
      staging host (or locally), create a `travelinbox-dev` application and token.
- [ ] Run both commands from the TravelInbox repository; capture output.
- [ ] Record in `docs/conformance/travelinbox-v1.md`: date, `service_version`, provider
      and models, live test result, evaluation numbers, token usage and cost, median and
      p95 job duration.
- [ ] File any failure as a job in the current epic or as a contract question (next job).

## Acceptance criteria

- The live test passes; the evaluation produces a number; both are recorded with the
  `service_version`.

## Out of scope

Improving the number (TravelInbox's prompts; and v0.3 evaluation harness for the
service's own corpus).

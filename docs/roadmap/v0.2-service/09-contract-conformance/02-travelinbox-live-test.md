# Job: TravelInbox live test and evaluation run

Version: v0.2 · Epic: 09 Contract conformance · Status: done · Depends on: 01, 08-02

## Goal

Run TravelInbox's own live test and evaluation against a deployment of the release
candidate with a real provider, and record the results.

## Context

Contract §14: `go test ./internal/extract -run TestServiceLive` and `make eval
EXTRACTOR=service` from TravelInbox's `backend/` with `EXTRACTION_URL` and
`EXTRACTION_TOKEN`; item 10 ("reports a no-correction share for flights and lodging,
whatever the number"). Decision 11 (corpus and stub are co-owned and imported).

## Tasks

- [x] Deploy the release candidate with the compose example and a real provider key on a
      staging host (or locally), create a `travelinbox-dev` application and token.
      Done locally with the binary (`vde serve` on loopback, not compose; build
      `vde-0.0.0-dev`), Nous Research `google/gemini-3.8-flash` for both roles.
- [x] Run both commands from the TravelInbox repository; capture output. Live test
      PASS (`corpus_end_to_end` skipped: no TravelInbox test database or object store);
      evaluation ran all 50 documents (exit 1 on TravelInbox's own targets).
- [x] Record in `docs/conformance/travelinbox-v1.md`: date, `service_version`, provider
      and models, live test result, evaluation numbers, token usage and cost, median and
      p95 job duration. All recorded in "Live test and evaluation run" except token usage
      and cost, which the service does not expose (D1).
- [x] File any failure as a job in the current epic or as a contract question (next job).
      No job failed (56 accepted, 0 rejected, 0 failed, no HTTP errors). The one service
      gap is listed under "Defects found" below, to be filed as a job.

## Acceptance criteria

- The live test passes; the evaluation produces a number; both are recorded with the
  `service_version`.

## Defects found

- **D1: provider token usage is not observable in `vde serve`.** The OpenAI-compatible
  adapter reads `usage.prompt_tokens` and `usage.completion_tokens` into the provider
  response (`internal/provider/openai/client.go`), but nothing downstream records them:
  `/metrics` has no token series, the worker's `job finished` log line has no usage fields,
  and the `jobs` table has no usage column. `docs/roadmap/backlog.md` ("Cost accounting per
  application") says "Metrics expose usage", which does not hold. Reproduction: run
  `vde serve` with any real provider, submit one job, wait for a terminal status, then
  `curl -s localhost:8090/metrics | grep -i token` prints nothing. Effect: this job's
  "token usage and cost" could not be recorded. Suggested fix (not done here): a counter
  `vde_provider_tokens_total{endpoint,model,role,direction}`.

## Out of scope

Improving the number (TravelInbox's prompts; and v0.3 evaluation harness for the
service's own corpus).

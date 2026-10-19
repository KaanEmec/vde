# Job: Serve wiring and the submit path

Version: v1.1 · Epic: 05 Long functions · Status: planned · Depends on: 02-04, 03-04

## Goal

`serveAs`, `createJob`, `readSubmission`/`readJSONSubmission` and `extractFile` are split
into named steps. Structure only.

## Context

Longest production functions at `78b6486`: `cmd/vde/serve.go:166` `serveAs` 254 lines
(wiring, servers, shutdown); `internal/api/submit.go:159` `createJob` 163;
`cmd/vde/extract.go:190` `extractFile` 147 (much of it moves to the local runner in
03-04); `submit.go:422` `readSubmission` 98; `submit_json.go:55` `readJSONSubmission` 93.
After epics 02 and 03 these are shorter; re-measure first. Suggested split for
`serveAs`: `buildDeps`, `startServers`, `drain`.

## Tasks

- [ ] Re-measure.
  - Skill: EVID.
  - Prompt: "List the 10 longest production functions with line counts and gocognit
    scores (go/ast script in the scratchpad plus `golangci-lint run --no-config
    --default=none --enable=gocognit`). Report only."
- [ ] Split.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: split serveAs into buildDeps, startServers
    and drain; split createJob and the two submission readers into named steps that
    follow the order of contract checks; split whatever is left of extractFile. Startup
    order, shutdown order and every response must stay. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make conformance`.

## Acceptance criteria

- No production function over 100 lines; `make check` and `make conformance` green.

## Out of scope

- Changing the order of request validation (which error a multi-fault request answers
  may change under the policy, but this job keeps it).

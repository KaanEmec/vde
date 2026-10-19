# Job: Serve wiring and the submit path

Version: v1.1 · Epic: 05 Long functions · Status: done · Depends on: 02-04, 03-04

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

- [x] Re-measure.
  - Skill: EVID.
  - Prompt: "List the 10 longest production functions with line counts and gocognit
    scores (go/ast script in the scratchpad plus `golangci-lint run --no-config
    --default=none --enable=gocognit`). Report only."
- [x] Split.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: split serveAs into buildDeps, startServers
    and drain; split createJob and the two submission readers into named steps that
    follow the order of contract checks; split whatever is left of extractFile. Startup
    order, shutdown order and every response must stay. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make conformance`.

## Acceptance criteria

- No production function over 100 lines; `make check` and `make conformance` green.

## Result

Re-measured at `614e49b` (go/ast line counts, `gocognit`): the longest production
functions were `serveAs` 232 lines, `createJob` 153, `runEval` 130, `extractFile` 102,
`readSubmission` 89, `readJSONSubmission` 87.

- `serveAs` opens the roles, store, blobs and PDF engine (the deferred closes stay
  there, so they still run last) and hands a `process` to `buildDeps` (warnings, audit,
  application sync with `logApplications`, the service_version function `appVersion`,
  `newWorkers`), `newServers` (HTTP, admin, metrics listener), `startServers` (pool and
  sweeper, then the listeners), `wait` and `drain` (servers, pool, webhooks, sweeper, in
  the old order).
- `createJob` keeps its order of checks as a list of steps: slot, key length, queue
  ceiling (unkeyed), `readBody`, options, judge policy, `replayKeyed` under the key lock,
  `limitSubmit`, `prepare` (url fetch, document, schema, representation),
  `chargeSubmit`, the job row and `insert` (store, the duplicate-key replay, queue count,
  header, audit, wake). The refusal closure is the function `refuse`. Its 15-line doc,
  which restated the code (02-04's leftover SIMPLIFY task), is now 7 lines.
- `readSubmission` checks each part with `(*submission).partSlot` before reading it;
  `readJSONSubmission` decodes, then `jsonDocument` (document and pages) and
  `(*jsonSubmission).textParts` (instructions, schema, options), in the old order.
- `extractFile`: the flag overrides are `(extractOptions).jobLimits`, the output
  `newJobOutput`.
- Not named by this job but over 100 lines: `runEval` (`cmd/vde/eval.go`), split into
  `(evalOptions).load` (flag checks, `--compare`, corpus) and `(evalOptions).runner`.

| Function | Lines before → after | gocognit before → after |
| --- | --- | --- |
| `serveAs` | 232 → 49 | 50 → 12 |
| `(*process).buildDeps`, `startServers`, `drain` (new) | 46, 43, 35 | 7, 6, 10 |
| `createJob` | 153 → 81 | 33 → 14 |
| `readSubmission` / `partSlot` (new) | 89 → 51 / 44 | 42 → 21 / 15 |
| `readJSONSubmission` / `textParts`, `jsonDocument` (new) | 87 → 23 / 42, 32 | 25 → 7 / 13, 7 |
| `runEval` / `load` (new) | 130 → 67 / 47 | 38 → 16 / 15 |
| `extractFile` | 102 → 61 | 23 → 12 |

The longest production function is now `printSummary` (`cmd/vde/config.go`, 89 lines).
No response, status, header or log line changed; errors and their order are as before.

Checks: `make check`, `make scenarios`, `make conformance` green; `make eval` identical
to `eval-reports/lean-baseline` (summary and every case, timings aside, same
`service_version`s); `make ci` green.

`git diff --stat`: production 5 files, +520 −363 (net +157: signatures, doc comments
and the `process` struct of the new steps; a pure split adds lines, so guardrail 5's
"net line count goes down" does not hold for this job).

## Out of scope

- Changing the order of request validation (which error a multi-fault request answers
  may change under the policy, but this job keeps it).

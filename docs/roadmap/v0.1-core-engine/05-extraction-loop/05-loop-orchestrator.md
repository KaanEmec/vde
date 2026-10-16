# Job: Loop orchestrator

Version: v0.1 · Epic: 05 Extraction loop · Status: done · Depends on: 01, 02, 03, 04, 03-*, 04-02, 04-04

## Goal

`extract.Run(ctx, Input) Outcome`: the bounded extractor-judge loop with the exact state
transitions and error codes of the contract, independent of HTTP and storage, reporting
progress (attempt count, candidate, feedback) through a small callback so the worker can
persist after every attempt.

## Context

Contract §6 (the loop, attempt limit, time limit), §7, §9 (job error codes). Architecture
overview, "Extraction loop". Brief: "A failed judge request never implies acceptance."

## Proposed types

```go
type Input struct {
    Representation document.Representation
    ExtractorInstructions, JudgeInstructions string
    Schema *Schema          // nil in schema-less mode
    MaxAttempts int
    Deadline time.Time      // computed from created_at by the caller
    OnAttempt func(Progress) // persisted by the worker; no-op in CLI
}
type Outcome struct {
    Status   Status          // Accepted, Rejected, Failed
    Result   json.RawMessage // last candidate or nil
    Feedback []Feedback
    Attempts int
    JudgeConfidence *float64 // from the last parsed verdict, nil when absent (decision 14)
    Error    *JobError       // code: provider_unavailable, extractor_failed, judge_failed, timeout, internal
}
```

## Tasks

- [x] Implement the loop as one readable function with helpers; no state machine
      framework. Attempt counter increments on every extractor call.
- [x] Extractor call errors: `ErrGroupUnavailable` → `failed/provider_unavailable`;
      permanent (refusal, empty) → count as attempt with `extractor_failed` if every
      attempt is unusable, otherwise continue if attempts remain; unparseable JSON →
      schema feedback and continue.
- [x] Schema invalid → feedback, no judge call, continue.
- [x] Judge: unparseable → one re-ask within the same attempt (same prompt plus a
      one-line reminder of the format); still unparseable → `failed/judge_failed`;
      group unavailable → `failed/provider_unavailable`; never acceptance on error.
- [x] `accept` → `Accepted` with advisory feedback and confidence; `reject` → feedback for
      next attempt (confidence kept for the outcome too);
      attempts exhausted → `Rejected` with last candidate and last feedback.
- [x] Deadline: any context deadline exceeded → `failed/timeout`, keeping candidate and
      feedback so far. Any other unexpected error → `failed/internal` (message generic,
      details logged without content).
- [x] Panics inside provider code are recovered into `internal`.
- [x] Scenario tests with the fake provider for every row of the exit-criterion list in
      the version README, asserting status, attempts, error code, judge call counts and
      that the extractor and judge saw identical document parts.

## Acceptance criteria

- All scenario tests pass; coverage of `internal/extract` above 90 percent.
- `Accepted` is only ever returned after a parsed `accept` verdict and a schema-valid
  candidate (a test enumerates every other path and asserts not accepted).

## Out of scope

Escalation to a stronger model (v0.3), persistence (v0.2 worker).

## Implementation notes

Done in `internal/extract/extract.go`. API: `Run(ctx, Input, Roles) Outcome`, with
`Roles{Extractor, Judge *router.Group; JudgeReparseTries, FeedbackMessageChars int}` built
by `NewRoles(cfg, build)` (one shared `*router.Group` when both roles name the same
group). Decisions:

- The job context is checked before every extractor call and after every role call
  error: a done context (deadline or cancellation) is `timeout`, checked before
  `router.ErrGroupUnavailable`, which can wrap a per-try timeout.
- `result` is the last parseable candidate and `feedback` the feedback on it: schema
  violations, the judge's entries, or empty while its verdict was pending. An answer that
  was not JSON is reported as feedback only when no candidate exists yet; the next
  extractor request then carries only that feedback, no previous candidate.
- A permanent extractor failure (refusal) is an unusable attempt and the next attempt
  repeats the same request; a permanent judge failure counts as an unparseable verdict.
- `OnAttempt` is called after each attempt that does not end the job.
- Panics anywhere in the loop (provider code, the callback) end as `internal` keeping the
  state so far; the stack is logged, never the panic value unless it is a runtime error.
- Coverage of `internal/extract` is 95 percent; `TestRun_neverAcceptedOtherwise`
  enumerates the paths that must not accept.

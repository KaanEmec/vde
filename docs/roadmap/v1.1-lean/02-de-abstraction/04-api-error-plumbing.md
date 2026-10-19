# Job: API error plumbing

Version: v1.1 · Epic: 02 De-abstraction · Status: done (SIMPLIFY and Gate left to the orchestrator) · Depends on: 02-03

## Goal

Request parsing in `internal/api` returns an ordinary `error`; repeated failure handling
is one helper. Structure only: every response (status, code, body, headers) stays the
same.

## Context

- Eight functions return `(T, apiError, bool)` and answer success with a dummy
  `newError(codeInternal, msgInternal), true` (9 sites): `submit.go:422, 596, 623`;
  `submit_json.go:55, 150, 179, 193`; `admin.go:169`. Make `apiError` implement `error`
  (or return `*apiError`) and drop the `e, ok` plumbing.
- Storage failures are handled five times (`submit.go:305-309, 333-336, 740-743,
  777-782, 811-816`, twice as identical `storeFailed` closures): one
  `s.storeUnavailable(w, r, what, err)`.
- `health.go:54-57, 107-125`: `/healthz` has its own token bucket; use
  `rateLimiter.takeBucket` with a new bucket kind.
- `jobjson.go:65-72` builds pointer timestamps inline; `optionalTime` exists in
  `admin.go:665`.
- `writeError(w, _ *http.Request, e)` ignores `r` at 52 call sites: drop the parameter
  in the same pass.
- About −70 lines.

## Tasks

- [x] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: in internal/api make apiError an error and
    change the eight (T, apiError, bool) functions to (T, error); add one storeUnavailable
    helper for the five storage-failure sites; reuse rateLimiter.takeBucket for /healthz;
    reuse optionalTime in jobjson.go; drop the unused *http.Request parameter of
    writeError with gopls. Every HTTP response must stay identical. Guardrails: skills.md."
    Then "/golang-refactoring Execute mode: step <n>."
- [ ] Simplify what is left in the submit path (orchestrator).
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/api/submit.go internal/api/submit_json.go — after the
    error change, remove leftover branches and comments that restate the code (the
    15-line doc of createJob). Do not split createJob yet (05-02). Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, PRTK, SEC.
  - Prompt: `/code-review high <first>^..<last>`, then
    `/pr-review-toolkit:review-pr errors`, then `/security-review` (request parsing and
    rate limiting changed).

## Acceptance criteria

- No `apiError, bool` return in `internal/api`; `make conformance` and the api e2e tests
  green with no expected response changed.

## Out of scope

- Error codes and messages (frozen codes; message texts may change but this job keeps
  them).

## Result

- `apiError` implements `error` (`code: message`, never request content); the eight
  `(T, apiError, bool)` functions return `(T, error)` and the nine dummy
  `newError(codeInternal, msgInternal), true` successes are `nil`.
- `writeError(w, err error)`: the unused `*http.Request` is gone at every call site; an
  error that is not an `apiError` is answered 500 internal without its text
  (`TestWriteErrorOther`); `TestErrorMessagesAreTemplates` checks the two-argument form.
- `s.storeUnavailable(w, r, what, err)` replaces the five storage-failure blocks (both
  `storeFailed` closures included); `queueRoom(w, r)` drains the body with `drainBody`,
  as the `refuse` closure does.
- `/healthz` takes its database-check tokens from `rateLimiter.takeBucket` (new
  `bucketHealthDB`, same rate and burst); one edge differs: a check whose result is not
  kept (a context error) no longer runs the bucket below zero.
- `jobjson.go` uses `optionalTime`.
- No expected response changed in any test; `make check`, `make conformance` and `make ci` green.

`git diff --stat` (Go): production 14 files, +177 −182 (net −5; about 50 of the changed
lines are the `writeError` parameter drop, +1 −1 each); tests 5 files, +26 −12 (net
+14). The −70 estimate is missed: the `, false` removals shorten lines rather than
delete them, and the createJob doc trim belongs to the SIMPLIFY task.

# Job: Job outcomes, error messages and the sweeper

Version: v1.1 · Epic: 03 Single source · Status: planned · Depends on: 03-01

## Goal

Job error messages, terminal outcomes, input deletion and error-code parsing each exist
once; the sweeper's three batch loops share one shape. Optionally the sweeper becomes a
file of `internal/worker`. Structure only.

## Context

- Error messages defined three times: `worker/pool.go:477-487` (`errorMessages`,
  `jobError`), `sweeper.go:321-322, 378-386` (`timeoutMessage`, `timeoutOutcome`),
  `extract.go:491-497` (`jobErrorMessages`). Export one `extract.NewJobError(code)`.
- Same logic in worker and sweeper: `pool.go:497-511` `fromRow` vs `sweeper.go:378-386`;
  `pool.go:463-475` `applyRetention` vs `sweeper.go:363-376` `clearInputs`;
  `pool.go:552-558` `errorCode` vs `audit.go:395-404` vs `admin.go:483-488`. The
  sweeper's copy drops token counts the worker keeps; the store's MAX upsert hides it
  (`job/store.go:739`), so it is not a bug today, but one helper removes the trap.
- `sweeper.go:246-361` `expired`, `removeRows`, `stale` share one loop (list a batch,
  loop, check `ctx.Err`, collect errors, stop on a short batch): one `batched` helper.
- `internal/sweeper` (430 lines) is used only by `serve` on the worker role and shares the
  logic above with `internal/worker`. Merging it as `worker/sweep.go` drops a package and
  the cross-package copies; a separate package is also defensible. Decide in the plan.
- About −90 lines (more with the merge).

## Tasks

- [ ] Decide on the merge.
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens) on internal/sweeper
    and internal/worker: does keeping the sweeper as its own package buy anything
    (separate role, separate tests, import cycles)? Recommend merge or keep, with the
    reason. Guardrails: skills.md."
- [ ] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: one extract.NewJobError(code); one helper
    each for terminal outcome from a row, input deletion under retention, and error-code
    parsing (used by worker, sweeper, audit, admin); one batched loop for the sweeper's
    three passes; <merge the sweeper into internal/worker as sweep.go | keep the package>
    as decided. Audit events and stored rows must stay identical. Guardrails: skills.md."
    Then "/golang-refactoring Execute mode: step <n>."
- [ ] Gate.
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review high <first>^..<last>`, then
    `/pr-review-toolkit:review-pr errors`.

## Acceptance criteria

- `grep` finds each job error message text once in production code; the worker,
  sweeper, retention and audit tests pass unchanged; `make check` green.

## Out of scope

- Changing retention or timeout behaviour.

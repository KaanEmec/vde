# Job: Job outcomes, error messages and the sweeper

Version: v1.1 · Epic: 03 Single source · Status: done (Gate task left to the orchestrator) · Depends on: 03-01

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

- [x] Decide on the merge (by evidence, without the CRITIC agent).
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens) on internal/sweeper
    and internal/worker: does keeping the sweeper as its own package buy anything
    (separate role, separate tests, import cycles)? Recommend merge or keep, with the
    reason. Guardrails: skills.md."
- [x] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: one extract.NewJobError(code); one helper
    each for terminal outcome from a row, input deletion under retention, and error-code
    parsing (used by worker, sweeper, audit, admin); one batched loop for the sweeper's
    three passes; <merge the sweeper into internal/worker as sweep.go | keep the package>
    as decided. Audit events and stored rows must stay identical. Guardrails: skills.md."
    Then "/golang-refactoring Execute mode: step <n>."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review high <first>^..<last>`, then
    `/pr-review-toolkit:review-pr errors`.

## Acceptance criteria

- `grep` finds each job error message text once in production code; the worker,
  sweeper, retention and audit tests pass unchanged; `make check` green.

## Out of scope

- Changing retention or timeout behaviour.

## Result

Merge decided by evidence: no import cycle (the sweeper imported a subset of the worker's
packages; nothing but `cmd/vde/serve.go` imported it), the same role wiring (`serve`
starts both on the worker role), and both test suites external packages whose only
collisions were three helpers (`rig`, `newRig`, identical `syncBuffer` and `sentinel`).
Keeping the package would have meant exporting `fromRow` and the input deletion from
`internal/worker` for one consumer. `internal/sweeper` is now `internal/worker/sweep.go`:
`worker.NewSweeper`, `worker.SweepOptions`, `worker.SweepResult`, `Sweeper` and `Locker`
unchanged; its tests are `sweep_test.go` (`sweepRig`) and `sweep_lock_test.go`; AGENTS.md,
the overview's layout and three rows of the conformance doc name the new place.

Diff of the copies (at fc2c76c):

- Messages: `worker` (3 codes), `sweeper` (`timeoutMessage`) and `extract` (5 codes) had
  the same texts. `extract.NewJobError(code)` is the one source; each text is once in
  production code.
- Terminal outcome from a row: the sweeper's `timeoutOutcome` equalled
  `fromRow(j, failed, timeout)` except that it left `InputTokens`/`OutputTokens` zero;
  `Finish` keeps the larger of row and outcome, so the stored row is the same. The
  sweeper uses `fromRow`; `TestTimeoutOutcomeFeedback` is `TestFromRowFeedback`
  (`pool_internal_test.go`).
- Input deletion: the worker's rule took `force_none` from the configuration or the
  stored application row, the sweeper's (a map built in `New`) from the configuration
  only. Kept as is here (`inputsAtTerminal` with the sweeper's configured application);
  the row's `force_none` for the sweeper is the separate `fix(sweeper)` commit after this
  one. One `deleteInputs` deletes for both; the worker's two log lines ("deleting blobs
  failed", "deleting inputs failed") are one, "worker: deleting inputs failed", whose
  `error` names the step ("delete blobs of <id>" or "delete inputs of <id>").
- Error-code parsing: `job.ErrorCode` replaces the worker's, the audit's and the admin
  route's copies. One edge differs: the admin copy ignored the unmarshal error, so a
  stored error whose `code` is a string but another field has the wrong type gave that
  code; now it gives "", as audit and worker always did. The service never writes such
  a row. `TestErrorCode` covers it.
- The sweeper's three passes (`expired`, `removeRows`, `stale`) share `batched`.

Checks: `make check`, `make scenarios`, `make conformance` green; `make eval` identical to
`eval-reports/lean-baseline` (timings and the removed `summary.cost_usd` aside). Audit
events and stored rows unchanged (the sweeper and worker suites pass with only the
renames).

`git diff --stat -M` (Go): production 9 files, +141 −185 (net −44); tests 4 files,
+62 −57 (net +5: `TestErrorCode`, the renames). Below the −90 estimate: the merge moves
code rather than removing it.

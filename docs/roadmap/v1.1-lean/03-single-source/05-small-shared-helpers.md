# Job: Small shared helpers

Version: v1.1 · Epic: 03 Single source · Status: done (Gate task left to the orchestrator; ground test loops skipped, see Result) · Depends on: 03-04

## Goal

Small helpers written several times exist once. Structure only.

## Context

- JSON-pointer escape and unescape four times: `extract/schema.go:333, 380-387`,
  `ground/ground.go:254-268`, `eval/compare.go:109-128`, validation in
  `extract/verdict.go:122`. Export them from `internal/ground` (lowest in the import
  graph) rather than a new package. About −25 lines.
- `internal/audit/audit.go:202-277` `Decode` and `deref`: two parallel 12-case switches.
  One table from kind to a generic `decodeAs[T]` replaces both and also feeds `Kinds()`
  (`:39-46`). About −45 lines.
- `internal/config` validators repeat "is not one of" 13 times and "between / positive /
  negative" about 25 times: a `v.oneOf(key, val, list)` and range helpers. About −30
  lines. Messages must stay identical (tests compare them).
- `internal/ground/{dates,numbers,normalize}_test.go`: three identical table loops; one
  helper. About −15 test lines.

## Tasks

- [x] JSON pointer.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/extract internal/ground internal/eval — keep one JSON
    pointer escape/unescape/split implementation, exported from internal/ground, and
    use it in the three other places. Guardrails: skills.md."
- [x] Audit decode table.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: replace the two switches in
    internal/audit Decode and deref with one kind-to-decoder table, and derive Kinds()
    from it; the strictness tests in audit, api, webhook, sweeper and worker must pass
    unchanged. Guardrails: skills.md."
- [x] Config validator helpers.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/config — introduce oneOf and range helpers on the
    validator for the repeated enum and range checks; every message text must stay
    byte-identical. Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- One JSON-pointer implementation; `audit.Decode` under 30 lines; `make check` green.

## Out of scope

- A new `internal/jsonptr` package.

## Result

Done by hand (no `/simplify`), each step behind `make check`.

- JSON pointer: five copies, not four. `internal/ground` exports `Pointer`,
  `SplitPointer`, `UnescapeToken` and `ValidPointer`; `extract` (schema feedback,
  subschema lookup, verdict validation and its fuzz test), `eval/compare.go`, the ground
  rules' `$ref` resolver and `internal/config/schema.go` use them (config now imports
  ground, which imports nothing internal). Differences found: the ground escape has a
  fast path for tokens without `~` or `/`, and two unescapes used a `strings.Replacer`;
  both give the same result for every input (`~1` before `~0`).
- Audit: one `kinds` table of `entry[T]()` (kind from `T.AuditKind()`, a generic
  decoder) replaces the two 12-case switches; `Kinds()` keeps documentation order from
  it. `Decode` is 17 lines. The audit, api, webhook and worker tests pass unchanged.
- Config: `v.oneOf(key, val, list)` (9 enum checks: provider, image target, PDF mode,
  HTML policy, logging level and format, the application HTML and PDF overrides,
  webhook events) and `v.between(key, n, lo, hi)` (5 range checks). Messages are
  byte-identical; the judge policy checks (typed, through `ParseJudgePolicy`), the
  "is not between" and "must be at least" wordings are left as they are.
- Skipped: the ground test helper. Only the two `Contains` loops in `dates_test.go` are
  identical; the `numbers` and `normalize` loops call other functions with other
  messages, and the job's tasks list no test change.

Checks: `make check`, `make scenarios` green; `make eval` identical to
`eval-reports/lean-baseline` (timings and the removed `summary.cost_usd` aside).

`git diff --stat` (Go): production 9 files, +113 −162 (net −49); tests 1 file, +3 −1
(the fuzz test's import).

# Job: Small shared helpers

Version: v1.1 · Epic: 03 Single source · Status: planned · Depends on: 03-04

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

- [ ] JSON pointer.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/extract internal/ground internal/eval — keep one JSON
    pointer escape/unescape/split implementation, exported from internal/ground, and
    use it in the three other places. Guardrails: skills.md."
- [ ] Audit decode table.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: replace the two switches in
    internal/audit Decode and deref with one kind-to-decoder table, and derive Kinds()
    from it; the strictness tests in audit, api, webhook, sweeper and worker must pass
    unchanged. Guardrails: skills.md."
- [ ] Config validator helpers.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/config — introduce oneOf and range helpers on the
    validator for the repeated enum and range checks; every message text must stay
    byte-identical. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- One JSON-pointer implementation; `audit.Decode` under 30 lines; `make check` green.

## Out of scope

- A new `internal/jsonptr` package.

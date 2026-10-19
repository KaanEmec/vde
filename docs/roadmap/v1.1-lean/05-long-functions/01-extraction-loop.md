# Job: The extraction loop and the verdict parser

Version: v1.1 · Epic: 05 Long functions · Status: planned · Depends on: 03-05

## Goal

`(*loop).run` reads as "extract once, then decide", with cognitive complexity under 25;
`parseVerdict` under 20. Structure only: every scenario, eval number and
`service_version` stays.

## Context

- `internal/extract/extract.go:250-387` `(*loop).run`: 138 lines, cognitive complexity
  54. Split into `l.extractOnce(ctx)` (attempt, parse, schema, lines 263-310) and
  `l.decide(ctx, candidate)` (grounding repair, `auto` gate and sampling, judge, lines
  312-385). Most branching is required by ADR 0009 and the frozen API (`judge` part,
  `extracted`, `verification`, `grounding` source): it moves, it does not go.
- `internal/extract/verdict.go:27` `parseVerdict`: 61 lines, complexity 31.
- Read ADR 0009 with its 2026-10-18 amendment and `testdata/scenarios/judge-policy/`
  first (AGENTS.md, "Before you change X").

## Tasks

- [ ] Plan the split.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: split internal/extract (*loop).run into an
    extract step and a decide step, and simplify parseVerdict, using gopls extract
    function. Read docs/architecture/decisions/0009-judge-policy-and-grounding.md and
    testdata/scenarios/judge-policy first. Bounded attempts, 'a failed judge call is
    never acceptance' and every outcome must stay. Guardrails: skills.md."
- [ ] Execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: step <n>; after each step run `make
    scenarios` and `make eval` with `--compare` against eval-reports/lean-baseline."
- [ ] Check failure paths.
  - Skill: PRTK (`silent-failure-hunter`).
  - Prompt: "Use the silent-failure-hunter agent on the diff of this job, with the rule
    that a failed or unparseable judge call must never end in `accepted`."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review max <first>^..<last>` (the loop is the core; use the highest
    effort).

## Acceptance criteria

- `gocognit` under 25 for `loop.run`, under 20 for `parseVerdict`; scenarios, eval
  numbers and `service_version` unchanged.

## Out of scope

- Removing escalation, second judge or classifier feedback (09-02).

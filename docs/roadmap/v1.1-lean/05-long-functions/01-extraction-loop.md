# Job: The extraction loop and the verdict parser

Version: v1.1 · Epic: 05 Long functions · Status: done · Depends on: 03-05

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

- [x] Plan the split.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: split internal/extract (*loop).run into an
    extract step and a decide step, and simplify parseVerdict, using gopls extract
    function. Read docs/architecture/decisions/0009-judge-policy-and-grounding.md and
    testdata/scenarios/judge-policy first. Bounded attempts, 'a failed judge call is
    never acceptance' and every outcome must stay. Guardrails: skills.md."
- [x] Execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: step <n>; after each step run `make
    scenarios` and `make eval` with `--compare` against eval-reports/lean-baseline."
- [ ] Check failure paths (orchestrator).
  - Skill: PRTK (`silent-failure-hunter`).
  - Prompt: "Use the silent-failure-hunter agent on the diff of this job, with the rule
    that a failed or unparseable judge call must never end in `accepted`."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review max <first>^..<last>` (the loop is the core; use the highest
    effort).

## Acceptance criteria

- `gocognit` under 25 for `loop.run`, under 20 for `parseVerdict`; scenarios, eval
  numbers and `service_version` unchanged.

## Result

`(*loop).run` is the attempt loop only: `resume` (limits, framings, the state of an
earlier run), then per attempt `extractOnce` (extractor call, parse, schema; the
not-JSON feedback in `notJSON`) and, for a schema-valid candidate, `decide` (grounding,
the one repair, the `auto` gate and sample, the judge through `judged`, which adds the
second judge). The attempt limit, the framings and the array flag are loop fields set
once. `decide` reads "does the candidate answer feedback" from `l.next` at the gate,
where it still holds what it held when the candidate arrived. Every judge error still
goes through `fail` with the same role (`judge`, `second_judge`), so a failed or
unparseable judge call ends `failed`, never `accepted`. `parseVerdict` hands each member
to `(*verdict).decodeMember`; error texts are unchanged.

| Function | Lines before → after | gocognit before → after |
| --- | --- | --- |
| `(*loop).run` | 138 → 23 | 54 → 10 |
| `(*loop).decide` (new) | 55 | 12 |
| `(*loop).extractOnce` (new) | 30 | 5 |
| `(*loop).judged`, `resume`, `notJSON` (new) | 19, 18, 17 | 5, 4, 3 |
| `parseVerdict` | 61 → 38 | 31 → 14 |
| `(*verdict).decodeMember` (new) | 29 | 11 |

Checks: `make check`, `make scenarios`, `make conformance` green; `make eval` identical
to `eval-reports/lean-baseline` for `always`, `never` and `auto` (summary and every
case, timings aside), with the same `service_version`s. Run in a clone of `HEAD` plus
these two files, because the working tree had another agent's provider edits in flight.

`git diff --stat`: production 2 files, +202 −140 (net +62: the new functions' doc
comments and signatures; nothing is deleted in a pure split, so guardrail 5's "net
line count goes down" does not hold for this job).

## Out of scope

- Removing escalation, second judge or classifier feedback (09-02).

# Job: `vde eval --url`

Version: v1.1 · Epic: 09 Deprecations · Status: dropped (owner decision L9, 2026-10-19: keep it) · Depends on: owner decision L9, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17); 03-04

## Goal

Per the owner's decision, remove the remote-service mode of `vde eval` or record why it
stays.

## Context

`vde eval --url` (`httpEngine`, `cmd/vde/eval.go:396-614`) plus `TestEval_url` and
`TestEval_urlJudge` (about 130 lines): about 350 lines. It evaluates a running deployment
over HTTP, which overlaps the conformance suite's deployment mode
(`VDE_CONFORMANCE_URL`) for checking a deployment, but not for measuring quality on a
deployment with its real configuration. The CLI is an operator surface: removal is a
CHANGELOG entry. `docs/operator/evaluation.md` documents the flag.

## Tasks

- [x] Owner decision L9 (2026-10-19): keep `vde eval --url`.
- [ ] If removed.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify cmd/vde/eval.go — remove the --url mode (httpEngine and its
    tests) and its documentation in docs/operator/evaluation.md; CHANGELOG 'Removed'.
    Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`, then `make eval`.

## Acceptance criteria

- Decision recorded; `make eval` green.

## Out of scope

- The local eval engine.

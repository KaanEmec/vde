# Job: Shared adapter test helpers

Version: v1.1 · Epic: 04 Providers · Status: planned · Depends on: 04-02

## Goal

Adapter tests share one set of helpers instead of five copies. Tests only.

## Context

Each adapter test file re-implements `serve`, `captured`, `endpoint`, `assertNoContent`
and `asProviderError`: `anthropic_test.go:30-130`, `gemini_test.go:35-95`,
`openai/client_test.go:28-75, 337`, `vertex_test.go:46-85`,
`classifier/client_test.go:22-31`. `anthropic/live_test.go:47` `onePagePDF` duplicates
`internal/pdf/pdftest`. Small `unparam` findings in tests: `anthropic_test.go:74`
(status always 200), `gemini_test.go:53` (timeout always 5 s). A
`internal/provider/providertest` package (test-support code, like `pdftest`) is the one
new package this version allows, because it replaces five copies. About −150 test lines.

## Tasks

- [ ] Execute.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/provider — move the repeated adapter test helpers
    (serve, captured, endpoint, assertNoContent, asProviderError) into
    internal/provider/providertest and use them in every adapter test; use pdftest in
    anthropic/live_test.go; fix the two unparam findings. Every test keeps its assertions.
    Guardrails: skills.md."
- [ ] Check coverage kept.
  - Skill: PRTK (`pr-test-analyzer`).
  - Prompt: "Use the pr-test-analyzer agent on the diff of this job: report any assertion
    or case that was lost while merging helpers."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`.

## Acceptance criteria

- `go test -cover ./internal/provider/...` coverage per adapter not lower than before.

## Out of scope

- Live tests' behaviour.

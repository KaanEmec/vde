# Job: Shared adapter test helpers

Version: v1.1 · Epic: 04 Providers · Status: done (coverage-analysis and Gate tasks left to the orchestrator) · Depends on: 04-02

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

- [x] Execute.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/provider — move the repeated adapter test helpers
    (serve, captured, endpoint, assertNoContent, asProviderError) into
    internal/provider/providertest and use them in every adapter test; use pdftest in
    anthropic/live_test.go; fix the two unparam findings. Every test keeps its assertions.
    Guardrails: skills.md."
- [ ] Check coverage kept (orchestrator).
  - Skill: PRTK (`pr-test-analyzer`).
  - Prompt: "Use the pr-test-analyzer agent on the diff of this job: report any assertion
    or case that was lost while merging helpers."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`.

## Acceptance criteria

- `go test -cover ./internal/provider/...` coverage per adapter not lower than before.

## Out of scope

- Live tests' behaviour.

## Result

`internal/provider/providertest` (85 lines) holds `Serve` (a recording `httptest` server
that closes with the test) with `Captured` (method, URL, header, body), `Reply`,
`Endpoint` (one endpoint parsed through `config.Parse`, every `api_key_env` resolving to
the given key), `AsError` and `AssertNoContent`. The anthropic, gemini, openai, vertex
and classifier tests use it; what stays per adapter is the one-line wiring that builds
that adapter's client (`serve` in gemini, openai and vertex), each package's list of
secrets, openai's `jsonBody` and vertex's `pathAndQuery`. The classifier tests that ran
their own servers (request shape, errors, auth) now record through `Serve` and assert
the same method, path, Content-Type, body and Authorization. `anthropic/live_test.go`
builds its PDF with `pdftest` instead of its own writer. `TestEveryProviderIsDocumented`
skips `providertest` (test support, not an adapter).

Unparam findings fixed: Anthropic's `reply` takes the body only (status was always 200),
Gemini's `newClient` no longer takes a timeout (always 5 s). Every test keeps its
assertions; the Vertex request-never-sent checks read `Captured.Method` instead of a
`called` flag.

Checks: `make check`, `make scenarios` and `make ci` green (in a local clone of `HEAD`
plus these files, as in 04-01). `go test -cover ./internal/provider/...` before this job
→ after: provider 98.8 → 98.8 %, anthropic 98.5 → 98.5, bedrock 98.9 → 98.9, classifier
96.2 → 96.2, fake 100 → 100, gemini 95.7 → 95.7, openai 97.1 → 97.1, vertex 97.4 → 97.4.
Before the epic (04-01's baseline): 98.2, 97.3, 98.9, 91.9, 100, 93.7, 95.1, 94.5.

`git diff --stat`: production 0; tests 8 files (the new package included), +246 −368
(net −122).

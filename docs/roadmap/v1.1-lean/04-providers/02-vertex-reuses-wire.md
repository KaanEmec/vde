# Job: Vertex reuses the Anthropic and Gemini wire code

Version: v1.1 · Epic: 04 Providers · Status: done (Gate task left to the orchestrator) · Depends on: 04-01 (Vertex stays, owner decision L8)

## Goal

`internal/provider/vertex` builds and parses requests with the Anthropic and Gemini
adapters' code instead of copies. The drift between the copies is settled first, in its
own commit, because one adapter's behaviour changes.

## Context

- `vertex/claude.go` (134 lines) copies `anthropic.go:54-256` ("as in
  internal/provider/anthropic (unexported there)"); `vertex/gemini.go` (132 lines)
  copies `gemini.go:49-213`.
- Drift found: Vertex blocks the `BLOCKLIST` and `SPII` finish reasons
  (`vertex/gemini.go:107`), direct Gemini does not (`gemini.go:188`); max tokens is
  checked `== 0` in one and `<= 0` in the other; malformed responses classify
  differently.
- Export `anthropic.Body`/`Parse` and `gemini.Body`/`Parse` (no new package). About −220
  production and −100 test lines. Vertex has never run live (STATUS.md).

## Tasks

- [x] Settle the drift (behaviour change, own commit).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: list every behavioural difference between
    vertex/gemini.go and gemini/gemini.go and between vertex/claude.go and
    anthropic/anthropic.go; propose which behaviour wins for each (refusing BLOCKLIST
    and SPII is the safer default for both). Stop for sign-off, then add a test per
    difference and align the code. Commit as
    `fix(provider): align Gemini and Vertex finish reasons`. Guardrails: skills.md."
- [x] Reuse the wire code.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: export the body builders and parsers of
    the anthropic and gemini adapters; make vertex call them and delete its copies and
    the tests that only covered the copies. Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- `vertex/claude.go` and `vertex/gemini.go` hold only Vertex-specific code (URL, auth);
  all adapter tests green.

## Out of scope

- Replacing `golang.org/x/oauth2` (kept while Vertex stays; see 09-02).

## Result

Drift between the copies, after 04-01 (sign-off: the orchestrator's brief settled each
in advance; fixed in `fix(provider): align Gemini and Vertex finish reasons`, one test
per difference, each failing on the old code):

| Difference | Settled as |
| --- | --- |
| Gemini finish reasons `BLOCKLIST`, `SPII`: refused on Vertex, answered directly | refused on both (`TestCompleteRefusals` cases) |
| Claude 2xx body cut off or empty: retryable directly (`json.Decoder` EOF), permanent on Vertex | permanent on both, as the four other adapters and `docs/providers/anthropic.md` say (`TestCompleteCutOffBody`) |
| Claude refusal, no text, malformed: status 0 directly, 200 on Vertex | status 0 on both, so the direct suite stays unchanged; Gemini keeps its status on both (`TestComplete_unusableAnswerStatus`) |
| Gemini request encoding error: plain error directly, `*provider.Error` on Vertex | `*provider.Error` (unreachable: these types always marshal) |
| Max tokens `== 0` against `<= 0` | already gone: 01-03 removed both fallbacks; the leftover `gemini.defaultMaxTokens` constant went in the reuse step |

Malformed Gemini answers were already permanent with the status in both, and 04-01 made
transport and read errors one code path.

Reuse: `anthropic.Body(req, vertexVersion)` and `anthropic.Parse(raw)`,
`gemini.Body(req)` and `gemini.Parse(raw, status)` are exported; the Messages API body
gained `anthropic_version,omitempty` and `model,omitempty` (the direct adapter always
has a model, so its body bytes are unchanged; `TestBodyVertexForm` pins the Vertex form).
`vertex/claude.go` and `vertex/gemini.go` are deleted; `vertex.go` keeps the URL, the
token, the model-family switch and the `anthropic_version` constant. Errors from the
shared code now read `anthropic: ...` and `gemini: ...` on Vertex too (kinds unchanged).
Removed tests that only covered the copies: `TestComplete_unusableAnswers` (the gemini
and anthropic suites cover each case; `TestComplete_unusableAnswerStatus` covers the
wiring) and `TestComplete_claudeOmitsZeroTemperature` (`TestComplete_claude` asserts no
temperature). Every remaining Vertex test, including the wire-body ones, passes unchanged.

Checks: `make check` and `make scenarios` green (in a local clone of `HEAD` plus these
files, as in 04-01). Coverage after 04-01 → after: anthropic 98.5 → 98.5 %, gemini
95.7 → 95.7, vertex 95.4 → 97.4; the others unchanged.

`git diff --stat`: fix commit production 4 files, +10 −15, tests 3 files, +45 −0; reuse
commit production 5 files, +50 −275 (net −225), tests 2 files, +12 −44.

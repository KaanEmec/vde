# Job: Vertex reuses the Anthropic and Gemini wire code

Version: v1.1 · Epic: 04 Providers · Status: planned · Depends on: 04-01; skip if 09-02 removes Vertex

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

- [ ] Settle the drift (behaviour change, own commit).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: list every behavioural difference between
    vertex/gemini.go and gemini/gemini.go and between vertex/claude.go and
    anthropic/anthropic.go; propose which behaviour wins for each (refusing BLOCKLIST
    and SPII is the safer default for both). Stop for sign-off, then add a test per
    difference and align the code. Commit as
    `fix(provider): align Gemini and Vertex finish reasons`. Guardrails: skills.md."
- [ ] Reuse the wire code.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: export the body builders and parsers of
    the anthropic and gemini adapters; make vertex call them and delete its copies and
    the tests that only covered the copies. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- `vertex/claude.go` and `vertex/gemini.go` hold only Vertex-specific code (URL, auth);
  all adapter tests green.

## Out of scope

- Replacing `golang.org/x/oauth2` (kept while Vertex stays; see 09-02).

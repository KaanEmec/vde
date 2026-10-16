# Job: Real-provider smoke test

Version: v0.1 · Epic: 06 CLI and developer experience · Status: planned · Depends on: 01, 02

## Goal

An opt-in test that exercises the Anthropic and OpenAI-compatible adapters end to end
with the sample documents, so adapter regressions are caught before a release without
making CI depend on provider keys.

## Context

ADR 0004. Working agreement: real provider calls only in an opt-in suite gated by
environment variables.

## Tasks

- [ ] `go test ./... -run TestLiveProviders` gated by `VDE_LIVE=1` and the respective
      key variables (Anthropic, OpenAI, OpenRouter, Gemini); skips cleanly otherwise.
- [ ] For each configured live provider: the four sample documents, with and without the
      sample schema; assert a terminal state, `attempts ≥ 1`, and for the text sample
      that the result contains a value visibly present in the document.
- [ ] A `make live` target and a line in `CONTRIBUTING.md` on running it before tagging.
- [ ] Record token usage per run to stderr for a cost sanity check.

## Acceptance criteria

- Skips without keys; passes with keys for at least one provider before v0.1 is tagged.

## Out of scope

Accuracy measurement (v0.3 evaluation harness).

# Job: Real-provider smoke test

Version: v0.1 · Epic: 06 CLI and developer experience · Status: in progress · Depends on: 01, 02

## Goal

An opt-in test that exercises the Anthropic and OpenAI-compatible adapters end to end
with the sample documents, so adapter regressions are caught before a release without
making CI depend on provider keys.

## Context

ADR 0004. Working agreement: real provider calls only in an opt-in suite gated by
environment variables.

## Tasks

- [x] `go test ./... -run TestLiveProviders` gated by `VDE_LIVE=1` and the respective
      key variables (Anthropic, OpenAI, OpenRouter, Gemini); skips cleanly otherwise.
- [x] For each configured live provider: the four sample documents, with and without the
      sample schema; assert a terminal state, `attempts ≥ 1`, and for the text sample
      that the result contains a value visibly present in the document.
- [x] A `make live` target and a line in `CONTRIBUTING.md` on running it before tagging.
- [x] Record token usage per run to stderr for a cost sanity check.

## Acceptance criteria

- Skips without keys; passes with keys for at least one provider before v0.1 is tagged.

## Out of scope

Accuracy measurement (v0.3 evaluation harness).

## Notes

`TestLiveProviders` in `cmd/vde/extract_live_test.go`, run by `make live`
(`VDE_LIVE=1 go test ./cmd/vde -run TestLiveProviders`). Keys: `ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY`; models default to those of the
examples and change with `VDE_LIVE_<PROVIDER>_MODEL`.

- Per provider it runs `vde extract` in process on all five samples (text, HTML, `.eml`,
  PDF with its page, JPEG), with and without the sample schema, asserting a terminal
  status and `attempts >= 1`, and for the text sample a result containing `HV-48213` or
  `412.5`. Each run prints status, attempts and the `--verbose` usage line (calls, input
  and output tokens) to stderr.
- When `testdata/user-docs/` exists, its PDFs (with the sample schema) and `.eml` files
  (schema-less) run too, named `user-doc-NN-<ext>`: only status, attempts, error code and
  usage are printed, never file names or content.
- Verified: skips cleanly without `VDE_LIVE` and with `VDE_LIVE=1` but no key. Not yet
  verified: a passing run with a key (none is set in the development environment); run
  `make live` with one key before tagging v0.1, then mark this job done.

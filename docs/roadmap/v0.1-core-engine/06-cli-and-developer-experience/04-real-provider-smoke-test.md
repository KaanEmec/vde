# Job: Real-provider smoke test

Version: v0.1 · Epic: 06 CLI and developer experience · Status: done · Depends on: 01, 02

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

## Outcome (2026-10-16)

Run with the OpenAI-compatible adapter against Nous Research (`google/gemini-3.8-flash`,
`VDE_LIVE_OPENAI_BASE_URL` override added for non-OpenAI hosts). Sample suite: text, HTML,
email, PDF and JPEG, with and without schema, all `accepted` (schema-less email first
failed three attempts because the 4096 default output-token cap truncated the answer; fixed
by `max_output_tokens`, default 16384, and truncation feedback; re-run `accepted` in one
attempt). Ten operator-supplied real documents (5 multi-page PDFs read natively, 5 emails):
all `accepted`, judge confidence 0.75 to 1.0. Typical cost per document: 2 to 3 model
calls, 4k to 10k input tokens for PDFs, 30k to 165k for raw-HTML emails (`visible_text`
policy cuts email input roughly 20 to 40 times). Anthropic, OpenAI and Gemini direct keys
were not available; those adapters are covered by httptest only.

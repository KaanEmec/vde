# Job: Development configuration example

Version: v0.1 · Epic: 06 CLI and developer experience · Status: done · Depends on: 01-03

## Goal

A documented minimal configuration: one provider, one model for both roles, one key in an
environment variable, so a consumer runs the engine (and from v0.2 the service) with a
single secret.

## Context

Contract §12: "A minimal development configuration MUST be documented: one provider, one
model for both roles, one application and token." The application and token parts are
added in v0.2.

## Tasks

- [x] `examples/vde.dev.yaml` for Anthropic with one model in both groups;
      `examples/vde.openrouter.yaml` using the OpenRouter preset; `examples/vde.gemini.yaml`;
      `examples/vde.local-ollama.yaml` for a fully local setup with an OpenAI-compatible
      endpoint; and `examples/vde.failover.yaml` showing primary and fallback per role
      across different providers.
- [x] `examples/README.md`: how to run `vde config check` and `vde extract` with each,
      which environment variable to set, and the expected output. (`vde extract` is
      documented as upcoming until epic 06 job 01 lands; update the README then.)
- [x] Sample inputs in `examples/samples/`: a synthetic text booking, a synthetic HTML
      email body, the same email as an `.eml` file with a PDF attachment, a one-page
      synthetic PDF with its rendered PNG, a JPEG photo of a synthetic receipt, generic
      extractor and judge instructions, and a small generic schema (not
      the TravelInbox one, which lives in `testdata/travelinbox/` for tests).
- [x] A test loads every example configuration through `config check` logic.
      (`internal/config/examples_test.go`: Parse with a dummy key, Validate, no errors;
      warnings allowed. A second test checks the sample files exist.)

## Acceptance criteria

- A newcomer follows `examples/README.md` and gets a result from `vde extract` with one
  key set.

## Notes

The acceptance criterion (a result from `vde extract` with one key) cannot be met until job 01
of this epic exists; everything else here is done. The PNG and JPEG were generated with a
throwaway Go program (`image/png`, `image/jpeg`, a hand-written 5x7 bitmap font) and the PDF
was written by hand; none of that code is committed.

## Out of scope

Production configuration guidance (v0.2 epic 10).

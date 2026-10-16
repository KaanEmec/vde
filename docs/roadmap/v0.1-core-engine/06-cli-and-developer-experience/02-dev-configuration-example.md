# Job: Development configuration example

Version: v0.1 · Epic: 06 CLI and developer experience · Status: planned · Depends on: 01-03

## Goal

A documented minimal configuration: one provider, one model for both roles, one key in an
environment variable, so a consumer runs the engine (and from v0.2 the service) with a
single secret.

## Context

Contract §12: "A minimal development configuration MUST be documented: one provider, one
model for both roles, one application and token." The application and token parts are
added in v0.2.

## Tasks

- [ ] `examples/vde.dev.yaml` for Anthropic with one model in both groups;
      `examples/vde.openrouter.yaml` using the OpenRouter preset; `examples/vde.gemini.yaml`;
      `examples/vde.local-ollama.yaml` for a fully local setup with an OpenAI-compatible
      endpoint; and `examples/vde.failover.yaml` showing primary and fallback per role
      across different providers.
- [ ] `examples/README.md`: how to run `vde config check` and `vde extract` with each,
      which environment variable to set, and the expected output.
- [ ] Sample inputs in `examples/samples/`: a synthetic text booking, a synthetic HTML
      email body, the same email as an `.eml` file with a PDF attachment, a one-page
      synthetic PDF with its rendered PNG, a JPEG photo of a synthetic receipt, generic
      extractor and judge instructions, and a small generic schema (not
      the TravelInbox one, which lives in `testdata/travelinbox/` for tests).
- [ ] A test loads every example configuration through `config check` logic.

## Acceptance criteria

- A newcomer follows `examples/README.md` and gets a result from `vde extract` with one
  key set.

## Out of scope

Production configuration guidance (v0.2 epic 10).

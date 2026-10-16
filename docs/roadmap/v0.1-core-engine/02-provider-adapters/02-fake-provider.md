# Job: Fake provider for tests

Version: v0.1 · Epic: 02 Provider adapters · Status: done · Depends on: 01

## Goal

A scripted provider that lets the whole engine and, later, the service be tested offline
with deterministic answers, failures and delays.

## Context

The roadmap's working agreement makes the fake the backbone of all tests. Real providers
are only hit by an opt-in suite.

## Tasks

- [x] `internal/provider/fake`: a provider constructed from a script, a slice of steps
      consumed in order (or matched by role, by inspecting the system framing). Each step
      is one of: answer text, error of a given kind and status, delay, then one of those.
- [x] Record every request (model, parts with media types and sizes, never asserting on
      full document bytes unless the test wants to) so tests can assert the extractor and
      judge saw the same representation.
- [x] A YAML or JSON script format so `vde extract --provider fake --script file` can
      replay scenarios from `testdata/scenarios/` without writing Go (used by epic 06).
- [x] Helpers for common scripts: accept first try, schema-invalid then valid, reject then
      accept, always fail retryable, always fail permanent.

## Acceptance criteria

- A unit test drives a two-step script and asserts the recorded requests.
- The fake is usable as an endpoint `provider: fake` in configuration when the binary is
  built with the default tags (it is tiny and has no secrets; no build tag gymnastics).

## Out of scope

Simulating token accounting beyond fixed numbers.

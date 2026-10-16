# Job: Acceptance checklist as automated tests

Version: v0.2 · Epic: 09 Contract conformance · Status: in progress · Depends on: epics 01–08

## Goal

A black-box test suite that starts the real binary (or container) with the fake provider
and the dev configuration and walks through the ten items of contract §14 over HTTP.

## Context

Contract §14 acceptance checklist; §2 to §12 MUSTs.

## Tasks

- [ ] `test/conformance/`: a Go test package that builds the binary in `TestMain`,
      starts `serve` on a free loopback port with a temp data directory and a fake
      provider script server (the fake provider as an HTTP server speaking the
      OpenAI-compatible shape, so the real adapter path is exercised), creates an
      application and tokens through the CLI, then runs one test per checklist item.
- [ ] A MUST traceability table in `docs/conformance/travelinbox-v1.md`: every MUST
      sentence of the contract (numbered by section) → test name(s). Gaps are visible.
- [ ] Container mode: the same suite runs against `docker compose up` with
      `VDE_CONFORMANCE_URL` set (CI job on the release branch).
- [ ] A `make conformance` target.

## Acceptance criteria

- All ten items pass in CI against the binary; the traceability table has no row without
  a test or an explicit "not testable automatically, verified by: …".

## Out of scope

Quality evaluation (next job).

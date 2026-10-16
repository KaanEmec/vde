# Job: End-to-end scenarios with the fake provider

Version: v0.1 · Epic: 06 CLI and developer experience · Status: planned · Depends on: 01, 02-02

## Goal

Executable scenario files that prove every behaviour in the v0.1 exit criterion through
the CLI, with no network, and that v0.2 reuses through the HTTP API.

## Context

Version README exit criterion; the roadmap's working agreement (fake provider as backbone).

## Tasks

- [ ] `testdata/scenarios/*.yaml`: provider script plus expected outcome (status,
      attempts, error code, judge call count, extractor call count per endpoint). Scenarios:
      accept first try; schema violation then accept (no judge call on attempt 1);
      judge reject then accept; three rejections → rejected with last feedback; extractor
      primary down → fallback serves; judge group down → `provider_unavailable`, never
      accepted; judge verdict garbage twice → `judge_failed`; slow extractor →
      `timeout` with partial candidate; schema-less accept; unparseable answer then
      accept; email `.eml` producing the same text part as its HTML body; judge
      confidence present and absent.
- [ ] A Go test runs every scenario through `extract.Run` directly and through the CLI
      binary (built in `TestMain`), comparing outcomes.
- [ ] `make scenarios` target for a human-readable run.

## Acceptance criteria

- All scenarios pass in CI without network access.

## Out of scope

Quality evaluation against ground truth (v0.3 evaluation harness).

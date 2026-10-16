# Job: End-to-end scenarios with the fake provider

Version: v0.1 · Epic: 06 CLI and developer experience · Status: done · Depends on: 01, 02-02

## Goal

Executable scenario files that prove every behaviour in the v0.1 exit criterion through
the CLI, with no network, and that v0.2 reuses through the HTTP API.

## Context

Version README exit criterion; the roadmap's working agreement (fake provider as backbone).

## Tasks

- [x] `testdata/scenarios/*.yaml`: provider script plus expected outcome (status,
      attempts, error code, judge call count, extractor call count per endpoint). Scenarios:
      accept first try; schema violation then accept (no judge call on attempt 1);
      judge reject then accept; three rejections → rejected with last feedback; extractor
      primary down → fallback serves; judge group down → `provider_unavailable`, never
      accepted; judge verdict garbage twice → `judge_failed`; slow extractor →
      `timeout` with partial candidate; schema-less accept; unparseable answer then
      accept; email `.eml` producing the same text part as its HTML body; judge
      confidence present and absent.
- [x] A Go test runs every scenario through `extract.Run` directly and through the CLI
      binary (built in `TestMain`), comparing outcomes.
- [x] `make scenarios` target for a human-readable run.

## Acceptance criteria

- All scenarios pass in CI without network access.

## Out of scope

Quality evaluation against ground truth (v0.3 evaluation harness).

## Notes

Fourteen scenarios in `testdata/scenarios/`, all run with `testdata/scenarios/config/vde.yaml`
(extractor group `primary` then `fallback`, judge group `judge`, all `provider: fake`).

- Format: each file is the fake provider script (unchanged format) as its first YAML
  document and, after `---`, the scenario (`input`, `expect`). `fake.Load` and
  `--provider-script` read only the first document, so every scenario file is also a
  script and the fake package was left unchanged. The fields are documented on the
  `scenario` type in `cmd/vde/extract_test.go`.
- `TestScenarios` (`cmd/vde/extract_test.go`) runs each through `extract.Run` directly and
  through the binary built in `TestMain`, checks status, attempts, error code, judge calls,
  extractor calls per endpoint, judge confidence and, when given, feedback, result and the
  document parts, and checks that both paths agree. CLI call counts come from the
  `--verbose` lines; the test also asserts that stderr carries no document or candidate
  content.
- Beyond the listed scenarios: a PDF with its page (sent natively) and a schema-less JPEG,
  covering every exit-criterion document type.
- The `.eml` scenario asserts its text part ends with the text of `booking.html` after
  normalising CRLF line endings (the message has CRLF, the HTML file LF).

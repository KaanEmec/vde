# Job: Shared e2e harness and test fakes

Version: v1.1 · Epic: 06 Tests and testdata · Status: planned · Depends on: 05-02

## Goal

The `cmd/vde` e2e tests start the service through one helper, and fakes copied between
packages exist once where Go allows it. Tests only.

## Context

- "Load config, listen on 127.0.0.1:0, run serve in a goroutine, cancel and drain, wait
  for /healthz" six times: `serve_test.go:57-90`, `serve_e2e_test.go:231-260`,
  `serve_role_test.go:49-80`, `storage_test.go:154-180`, `webhook_e2e_test.go:48-80`,
  `token_test.go:64-110`. `startServiceAdmin` exists but only two files use it. Two HTTP
  helpers (`call`, `request`). One `startServe(t, cfgPath, opts)`. About −115 lines.
- Fakes copied across packages: `stubPDF`/`stubPDFDoc` (api, worker; may go in 02-02),
  `pngBytes`/`pngPage`, `failingBlobs` (api, sweeper), store helpers (`newTestStore`,
  `openStore`), `syncBuffer` (cmd/vde, worker, sweeper) and `lockedBuffer` (logging).
  A shared test package would remove about 100 lines but adds a package: decide in the
  plan (default: no new package; dedupe within each package only).
- Large tests worth a table: `TestSubmitIdempotency` (214), `TestNoSentinelInLogs`
  (210), `TestRunConfigCheck` (163), `TestSelect` (120), `TestSelectWith_pdfPolicy`
  (131), `TestGroup_Complete` (178).
- Both OpenAPI superset checkers (`openapi_test.go:190` `assertSuperset`,
  `openapi_snapshot_test.go:59` `assertTreeSuperset`) overlap; they guard the freeze, so
  merge them only if the merged one is proven to catch the same losses (a lost enum
  value, property, header, error code, required field).

## Tasks

- [ ] e2e harness.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify cmd/vde — replace the six copies of the serve start-up in the
    _test.go files with one startServe(t, cfgPath, opts) helper and one HTTP request
    helper; keep every assertion. Guardrails: skills.md."
- [ ] Table-drive the largest tests where cases share a body.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/api/submit_test.go internal/document/representation_test.go
    internal/router/router_test.go cmd/vde/config_test.go — turn repeated blocks in the
    largest tests into named table cases; standard testing, no testify; keep every case.
    Guardrails: skills.md."
- [ ] OpenAPI checkers (only with proof).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: merge assertSuperset and assertTreeSuperset
    into one checker, and add a test per loss type (enum value, property, header, error
    code, required field) proving the merged checker fails on each. Stop if any loss is
    not caught. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review medium <first>^..<last>`, then
    `/pr-review-toolkit:review-pr tests`.

## Acceptance criteria

- One serve start-up helper in `cmd/vde` tests; total test lines down by at least 300;
  coverage per package not lower than the baseline.

## Out of scope

- Removing the Postgres and S3 suites.

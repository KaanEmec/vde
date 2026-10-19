# Job: Shared e2e harness and test fakes

Version: v1.1 · Epic: 06 Tests and testdata · Status: done (test lines −155, short of −300: see Result) · Depends on: 05-02

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

- [x] e2e harness (by hand, no /simplify).
  - Skill: SIMPLIFY.
  - Prompt: "/simplify cmd/vde — replace the six copies of the serve start-up in the
    _test.go files with one startServe(t, cfgPath, opts) helper and one HTTP request
    helper; keep every assertion. Guardrails: skills.md."
- [x] Table-drive the largest tests where cases share a body (little left: see Result).
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/api/submit_test.go internal/document/representation_test.go
    internal/router/router_test.go cmd/vde/config_test.go — turn repeated blocks in the
    largest tests into named table cases; standard testing, no testify; keep every case.
    Guardrails: skills.md."
- [x] OpenAPI checkers (only with proof).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: merge assertSuperset and assertTreeSuperset
    into one checker, and add a test per loss type (enum value, property, header, error
    code, required field) proving the merged checker fails on each. Stop if any loss is
    not caught. Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review medium <first>^..<last>`, then
    `/pr-review-toolkit:review-pr tests`.

## Acceptance criteria

- One serve start-up helper in `cmd/vde` tests; total test lines down by at least 300;
  coverage per package not lower than the baseline.

## Result

- **One serve start-up.** `startServe(t, cfgPath, serveOptions{role, build, getenv,
  logs, admin})` in `cmd/vde/serve_test.go` loads the configuration, listens on loopback
  (and the admin port), runs `serveAs`, waits for `/healthz` and returns both base URLs
  and an idempotent `stop` that checks `serveAs` returned nil within `e2eWait(10 s)`
  (also at cleanup). It replaces `startService`, `startServiceAdmin`, the `serve`
  wrapper and the copies in `TestServeAndShutdown`, `TestServeRoles`,
  `TestServeJudgePolicies` (the judge fake is now chosen by its model name instead of by
  endpoint pointer), `TestServeSyncExtractAndWebhook` and `TestStorageEndToEnd`.
  `serveWith(p)` answers every endpoint with one provider. Also one `waitJob` (poll a job
  to its terminal state; four copies) and one `jobsToken` (five copies); `request` is
  `call` with a small body. `serve_log_e2e_test.go` starts the real binary and keeps its
  own start-up.
- **Tables.** `TestSubmitIdempotency`, `TestNoSentinelInLogs`, `TestRunConfigCheck`,
  `TestSelect`, `TestSelect_pdfPolicy` and `TestGroup_Complete` are already tables or
  subtests with distinct bodies; only `TestRunConfigCheck`'s three cases on the same file
  became one. No new shared test package (fakes copied across packages stay; within a
  package there were no copies).
- **OpenAPI checkers merged, with proof.** `supersetProblems(base, cur, relaxed)`
  (`internal/api/openapi_snapshot_test.go`) replaces `assertSuperset` and
  `assertTreeSuperset`: paths, responses and schemas joined the never-drop collections, a
  missing HTTP method is a lost operation, the top-level required rule (with `relaxed`)
  and the error-code comparison moved in, and it returns its findings. It now also
  checks the TravelInbox document (which passes: fields, headers, enums and error codes
  of theirs are all in ours, stricter than before). `TestSupersetProblemsCatchLosses`
  removes, from a copy of the 1.0.0 snapshot, a path, an operation, a response, a
  schema, a property, a header, an enum value, an error code, an error code's status, a
  top-level and a nested required field, and adds a required request field: each is
  reported.

Coverage (`go test -cover`): `cmd/vde` 81.3 % → 81.3 %, `internal/api` 91.3 % → 91.3 %.
Test lines −155 (+320 −475): `cmd/vde` −188, the OpenAPI merge +33 (its
proof test). The −300 target assumed table-driving tests that already are tables, or a
shared test package (declined). `make check` green.

`git diff --stat`: production 0 files; tests 11 files (+320 −475); testdata 0.

## Out of scope

- Removing the Postgres and S3 suites.

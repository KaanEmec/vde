# Job: Test-only and unused symbols

Version: v1.1 · Epic: 01 Dead code · Status: planned · Depends on: 00-04

## Goal

No production file declares a function that only tests call, and no parameter or result
is unused. Structure only; no behaviour change.

## Context

`deadcode ./cmd/...` lists 24 functions reachable only from tests; `deadcode -test ./...`
lists none, so each is either moved to a `_test.go` file, replaced by the production
variant in tests, or kept with a reason. Audit findings (line numbers at `78b6486`):

| Symbol | Action |
| --- | --- |
| `cmd/vde/serve.go:153` `serve` | Move to a `_test.go` file (5 tests use it) |
| `internal/api/auth.go:32` `ScopesFrom`, `:16` `auditAuthFailed`; `submit.go:381` `submitSlots.held` | Move to `_test.go` files |
| `internal/logging/logging.go:51` `AllowedKeys` | Remove; the test reads `allowed` |
| `internal/extract/version.go:68` `ApplicationServiceVersion`, `:92` `ApplicationServiceVersions` | Remove; rename `ApplicationServiceVersionFor` to `ApplicationServiceVersion`; tests use it |
| `internal/document/representation.go:118-161` `Select`, `SelectWith`, `SelectContext` | One `Select(ctx, …, pdf PDFPolicy)`; drop the variadic whose doc says only the first is used; about 25 test call sites and `internal/extract/doc.go:17` |
| `internal/pdf/pdf.go:161` `(*Engine).Workers` | Remove (no reference at all) |
| `internal/provider/fake/script.go` `AcceptFirstTry`, `SchemaInvalidThenValid`, `RejectThenAccept` | Inline into their one test each; keep `Answer`, `Fail`, `AlwaysRetryable`, `AlwaysPermanent` (used across packages) |
| `internal/ground/normalize.go:114` `Text.Contains` | Move to a `_test.go` file |
| `internal/config/config.go:396` `Load` | Remove, or make `cmd/vde` use it instead of its own `Parse` call; pick the one that deletes more |
| `internal/api/quota.go:88` `quotaHeaders` | Return `bool` only (the `quotaState` result is unused) |
| `internal/api/submit.go:325` `queueRoom` | Drop the unused `w` parameter |
| `internal/api/submit_json.go:192` `fetchDocument` nil check | Fold into `fetchPending`; the caller already required `s.fetch != nil` |
| `cmd/vde/healthcheck.go:83` `X-Forwarded-Proto` header | Remove; `requireHTTPS` lets `GET /healthz` through (`middleware.go:118`); fix the stale comment there |
| `cmd/vde/config.go:121` `loadConfig` | Inline into its two callers |
| `internal/auth/token.go:110` `Permits` | Merge the two identical cases |
| `internal/audit/audit.go:202` `Decode`, `deref` | Keep (tests in five packages use them as a strictness check); compressed in 03-05 |
| `internal/webhook/webhook.go:245` `Verify` | Keep as the test oracle; fix its comment ("receivers written in Go can use it" is false for an `internal/` package) |
| Production comments naming roadmap items: `serve.go:61`, `admin.go:21`, `api.go:26,34-36,55,69`, `middleware.go:28`, `errors.go:109`, `sweeper.go:43`, `pool.go:34,80` | Remove the roadmap reference; drop "Later epics add their fields here" |

## Tasks

- [ ] Refresh the inventory and confirm each row with references.
  - Skill: EVID.
  - Prompt: "Run `deadcode ./cmd/...`, `deadcode -test ./...` and
    `golangci-lint run --no-config --default=none --enable=unused,unparam ./...`. For each
    symbol in the table of docs/roadmap/v1.1-lean/01-dead-code/01-test-only-symbols.md, run
    `gopls references` and grep `docs/` and `*.md` for its name. Report a table: symbol,
    production refs, test refs, doc refs, still valid yes/no. Change nothing."
- [ ] Plan the order (one commit per package group: cmd/vde, api+auth, extract+document+pdf,
      ground+fake, config+logging).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: remove or move the test-only and unused symbols
    in the table of docs/roadmap/v1.1-lean/01-dead-code/01-test-only-symbols.md, using the
    reference report above. Group steps by package; for renames use gopls rename. No
    worktrees or PRs; commits follow AGENTS.md. Guardrails: skills.md."
- [ ] Execute the plan, one step at a time.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: step <n> of the approved plan. Run `make
    check` after the step. Guardrails: skills.md."
- [ ] Single `document.Select` (the largest step: about 25 test call sites).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: replace document.Select, SelectWith and
    SelectContext with one `Select(ctx, doc, extractor, judge, html, images, pdf
    PDFPolicy)`; update every caller and test with gopls; update the example in
    internal/extract/doc.go. Run `make check` and `make scenarios`. Guardrails: skills.md."
- [ ] Remove the stale roadmap references in comments touched above.
  - Skill: PRTK (`comment-analyzer`).
  - Prompt: "Use the comment-analyzer agent on the diff of this job: report comments that
    name roadmap epics or jobs, describe removed code, or are now false. Go project; follow
    AGENTS.md. Guardrails: skills.md." Then fix the findings by hand.
- [ ] Gate.
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review high <first>^..<last>`, then
    `/pr-review-toolkit:review-pr comments errors`.

## Acceptance criteria

- `deadcode ./cmd/...` lists only `audit.Decode`, `audit.deref`, `webhook.Verify`, the
  kept `fake` helpers and `ContentLogger.Log` (removed in 01-02), each with its reason
  recorded here.
- `golangci-lint` `unparam` reports nothing in production code.
- `make check`, `make scenarios`, `make conformance` green.

## Out of scope

- The legacy shim at `internal/extract/extract.go:259-262` that infers `repaired` for
  rows stored before v0.4: deployments of v0.3.0 may still hold such rows.
- Unexporting identifiers that are used only in their own package (`job.SourceAdmin`,
  `config.SSEAES256`, ...): cosmetic.

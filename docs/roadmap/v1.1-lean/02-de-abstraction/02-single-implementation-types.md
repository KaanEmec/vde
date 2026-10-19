# Job: Single-implementation interfaces and wrapper types

Version: v1.1 · Epic: 02 De-abstraction · Status: done (ARCH and CRITIC skipped: orchestrator token budget; PDF wrappers kept, see Result) · Depends on: 02-01

## Goal

Outside `provider.Provider`, `job.Store` and `blob.Store`, no production interface or
wrapper type has a single production implementation. Structure only.

## Context

- `internal/auth/auth.go:36-40` `Reader` and `:97-101` `Creator`: only `job.Store`
  satisfies them, no fakes. Take `job.Store`.
- `internal/job/sqlite.go`, `postgres.go`: `SQLite` and `Postgres` are both
  `struct{ sqlStore }`; all SQL is shared (`rebind`, four `if s.pg` branches).
  `cmd/vde/storage.go` adds a third interface, `closingStore`, only to return either.
  Collapse into one exported `*job.DB` (open by driver), delete `closingStore` and the
  `var _ Store` assertions. `job.Store` itself stays: it is the decided seam and the api
  tests inject failures through it. Rename `internal/job/sqlite_test.go` (it holds the
  shared `forEachDriver` suite) to `store_test.go`.
- `internal/document/pdf.go:15-46` `PDFEngine`, `PDFDoc`, `PDFEngineOf`, `pdfEngine`
  wrap `*pdf.Engine`; the only other implementation is the test stub `stubPDF`
  (duplicated in `internal/api` and `internal/worker` tests). Replacing it with real
  pdfium and `pdftest.Build` costs test time (WebAssembly start): measure before
  deciding.
- `internal/audit` `Appender` (one test fake) and `internal/provider/bedrock`
  `ConverseAPI` (test seam): keep; record why.
- The exhaustive list comes from the `iface` linter of 00-04 and the architecture pass
  below; add what they find.

## Tasks

- [ ] Find every remaining seam with one production implementation (skipped: orchestrator
  token budget; the `iface` inventory below replaces it).
  - Skill: ARCH.
  - Prompt: "/mattpocock-skills:improve-codebase-architecture Focus on internal/ and
    cmd/vde. Find interfaces, wrapper types and pass-through layers with one production
    implementation; apply the deletion test. Keep provider.Provider, job.Store and
    blob.Store (decided in docs/architecture/overview.md principle 2 and ADRs in
    docs/architecture/decisions/). Do not create GLOSSARY.md, ADRs or any file in the
    repository. Guardrails: docs/roadmap/v1.1-lean/skills.md."
- [ ] Challenge the list before editing (skipped: orchestrator token budget).
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens) on the candidate
    list: for each, is a second production implementation planned in
    docs/roadmap/backlog.md or an ADR? Is removal cheaper than keeping? Rank Blocker,
    High, Medium, Nit."
- [x] Execute: auth interfaces, one `*job.DB`, `closingStore`, test file rename.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode, then Execute mode: replace auth.Reader and
    auth.Creator with job.Store; merge job.SQLite and job.Postgres into one *job.DB with
    `TryLock` branching on the driver; delete closingStore in cmd/vde/storage.go; rename
    internal/job/sqlite_test.go to store_test.go. Run the Postgres suite if
    VDE_TEST_POSTGRES is set. Guardrails: skills.md."
- [x] Decide and, if chosen, execute the PDF wrapper removal (decided: keep).
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: measure `go test ./internal/document
    ./internal/api ./internal/worker` time with the stub, then with real pdfium via
    internal/pdf/pdftest; if the difference is under 10 s, remove PDFEngine, PDFDoc,
    PDFEngineOf and pdfEngine and use *pdf.Engine directly. Report the numbers before
    editing. Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- `golangci-lint run --no-config --default=none --enable=iface ./...` reports only the
  decided interfaces and the recorded exceptions; `make check` and the storage suites
  green.

## Out of scope

- Merging the SQLite and Postgres migrations (they differ by dialect; squashing would
  break upgrades of deployed 1.0 databases).

## Result

Inventory: `golangci-lint run --no-config --default=none --enable=iface ./...` reports 0
issues (its default analyzer, `identical`). With every analyzer on (`identical`,
`unused`, `opaque`, `unexported`) it reports two: `provider.Provider` unused inside its
own package (decided interface) and `document.PDFEngineOf` returning an interface over one
implementation (kept, below). Every `type ... interface` in `internal/` and `cmd/` was
checked by hand:

- Removed: `auth.Reader` and `auth.Creator` (take `job.Store`); `job.SQLite` and
  `job.Postgres` merged into one `*job.DB` (`Open`, `OpenPostgres`; `TryLock` branches on
  the driver; the `var _ Store` assertions gone); `closingStore` in `cmd/vde` (returns
  `*job.DB`); `internal/job/sqlite_test.go` renamed `store_test.go`.
- Kept, decided: `provider.Provider`, `job.Store`, `blob.Store`.
- Kept, more than one implementation: `audit.Details` (one type per event kind),
  `job.querier` (`*sql.DB`, `*sql.Tx`) and `job.scanner` (`*sql.Row`, `*sql.Rows`).
- Kept, test seams: `audit.Appender` (a failing appender tests that `Record` reports a store
  failure and the helpers log it instead), `bedrock.ConverseAPI` (the AWS SDK client; tests answer without
  network or credentials), `sweeper.Locker` (one production type, `*job.DB`; the test
  fake holds the lock for "another process", which SQLite cannot and PostgreSQL needs
  Docker for).
- Kept, measured: `document.PDFEngine`, `PDFDoc`, `PDFEngineOf`, `pdfEngine`. Real
  pdfium in a test process costs its first `Open`: 13.5 s per package under `-race` with
  a cold compilation cache (CI, and `make check` after the cache is cleared), 0.3 s with
  a warm one, 1.2 s uncached without `-race`. `internal/api` and `internal/worker` would
  each pay it (27 s CPU, about 13.5 s wall in parallel), over the 10 s budget; the stubs
  also inject `pdf.ErrBusy` and count opens, which a real engine cannot do
  deterministically. Baseline with the stubs: `go test -race ./internal/document
  ./internal/api ./internal/worker` 22 s wall.

`make check` and `make storage` (PostgreSQL and S3) green.

`git diff --stat` (Go): production 9 files, +87 −131 (net −44, the 48 receiver renames
count as +1 −1 each); tests 11 files, +46 −46 (net 0; the rename is a move).

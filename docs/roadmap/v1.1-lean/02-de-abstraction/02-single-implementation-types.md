# Job: Single-implementation interfaces and wrapper types

Version: v1.1 · Epic: 02 De-abstraction · Status: planned · Depends on: 02-01

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

- [ ] Find every remaining seam with one production implementation.
  - Skill: ARCH.
  - Prompt: "/mattpocock-skills:improve-codebase-architecture Focus on internal/ and
    cmd/vde. Find interfaces, wrapper types and pass-through layers with one production
    implementation; apply the deletion test. Keep provider.Provider, job.Store and
    blob.Store (decided in docs/architecture/overview.md principle 2 and ADRs in
    docs/architecture/decisions/). Do not create GLOSSARY.md, ADRs or any file in the
    repository. Guardrails: docs/roadmap/v1.1-lean/skills.md."
- [ ] Challenge the list before editing.
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens) on the candidate
    list: for each, is a second production implementation planned in
    docs/roadmap/backlog.md or an ADR? Is removal cheaper than keeping? Rank Blocker,
    High, Medium, Nit."
- [ ] Execute: auth interfaces, one `*job.DB`, `closingStore`, test file rename.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode, then Execute mode: replace auth.Reader and
    auth.Creator with job.Store; merge job.SQLite and job.Postgres into one *job.DB with
    `TryLock` branching on the driver; delete closingStore in cmd/vde/storage.go; rename
    internal/job/sqlite_test.go to store_test.go. Run the Postgres suite if
    VDE_TEST_POSTGRES is set. Guardrails: skills.md."
- [ ] Decide and, if chosen, execute the PDF wrapper removal.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: measure `go test ./internal/document
    ./internal/api ./internal/worker` time with the stub, then with real pdfium via
    internal/pdf/pdftest; if the difference is under 10 s, remove PDFEngine, PDFDoc,
    PDFEngineOf and pdfEngine and use *pdf.Engine directly. Report the numbers before
    editing. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- `golangci-lint run --no-config --default=none --enable=iface ./...` reports only the
  decided interfaces and the recorded exceptions; `make check` and the storage suites
  green.

## Out of scope

- Merging the SQLite and Postgres migrations (they differ by dialect; squashing would
  break upgrades of deployed 1.0 databases).

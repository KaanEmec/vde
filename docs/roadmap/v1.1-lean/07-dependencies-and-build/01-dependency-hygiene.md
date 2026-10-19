# Job: Dependency hygiene and a local ULID

Version: v1.1 · Epic: 07 Dependencies and build · Status: done · Depends on: 00-04

## Goal

`go.mod` lists only what the code needs, every direct dependency has a row in the
overview's "Implementation decisions" (AGENTS.md: "No new dependency without an ADR and a
row"), and `oklog/ulid` is replaced by about 35 lines of standard library code.

## Context

- 17 direct dependencies. Rows missing from the overview's table, although the
  dependencies shipped: `aws-sdk-go-v2` and `smithy-go` (S3, Bedrock), `jackc/pgx/v5`,
  `golang.org/x/oauth2` (and `cloud.google.com/go/compute/metadata`), `golang.org/x/image`.
- `oklog/ulid/v2` is used by `internal/job/job.go` `NewID` and the request id in
  `internal/api/middleware.go`; about 20 KB. A local version: millisecond timestamp plus
  `crypto/rand` in Crockford base32, monotonic within a millisecond, the same
  26-character format (the blob shard uses the last two characters).
- `gopkg.in/yaml.v3` may be unmaintained upstream (continued as `go.yaml.in/yaml/v3`):
  verify; switching is an import change, but it is a new module path, so it needs the
  overview row.
- Keep: pgx, modernc sqlite, go-pdfium and wazero, jsonschema, x/text, x/net, x/image
  (no standard library alternative, or ADR-decided). The cloud SDKs are 09-02's
  question.

## Tasks

- [x] Inventory (by hand: `go mod graph`, `go version -m`, the module sources).
  - Skill: GODEPS.
  - Prompt: "/golang-dependency-management For each direct require in go.mod: `go mod
    why -m`, importing packages, approximate binary weight, maintenance status (last
    release), and whether docs/architecture/overview.md 'Implementation decisions' has a
    row. Check whether gopkg.in/yaml.v3 is still maintained and what replaces it. Report
    only; change nothing."
- [x] Replace ULID.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: replace oklog/ulid/v2 with a local
    newULID in internal/job (exported for internal/api's request id): same 26-character
    Crockford base32, millisecond time, crypto/rand, monotonic within a millisecond, with
    a test for format, ordering and uniqueness under concurrency; `go mod tidy`.
    Guardrails: skills.md (this removes a dependency: record it in the overview table)."
- [x] Align the overview's dependency table with `go.mod`.
  - Skill: none (Edit).
  - Prompt: "Add rows to docs/architecture/overview.md 'Implementation decisions' for
    every direct dependency that lacks one (purpose, ADR or job that brought it), remove
    the ULID row, and the yaml row if it was switched. Targeted edits only."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, GODEPS.
  - Prompt: `/code-review high <first>^..<last>`, then
    "/golang-dependency-management run govulncheck ./... and report."

## Acceptance criteria

- `go mod tidy` leaves no diff; the overview table and `go.mod` list the same direct
  dependencies; `govulncheck` clean.

## Result

- **Inventory.** 17 direct requirements before. `oklog/ulid/v2` was used only by
  `job.NewID` and the request id. `gopkg.in/yaml.v3` (v3.0.1, 2022-05-27) is
  unmaintained: its repository is archived and the YAML organisation continues it as
  `go.yaml.in/yaml/v3`, of which v3.0.5 (2026-07-26) was already in the module graph
  (required by go-pdfium and prometheus/common, not linked). The gofmt-normalized sources
  of v3.0.1 and v3.0.5 differ only in: an unhashable merge key is a parse error instead of
  a runtime panic; new encoder options (`CompactSeqIndent`, off by default); a line break
  after a line comment in the emitter, reached only when encoding `yaml.Node` comments,
  which vde never does (it marshals plain structs). The overview lacked rows for
  `aws-sdk-go-v2`, `smithy-go`, `jackc/pgx/v5`, `golang.org/x/oauth2` (with
  `cloud.google.com/go/compute/metadata`) and `golang.org/x/image`.
- **ULID.** `internal/job/ulid.go` (63 lines with comments): `NewULID` encodes a 48-bit
  millisecond time and 80 bits from `crypto/rand` as 26 Crockford base32 characters; in
  the same millisecond, or when the clock steps back, the previous random part is
  incremented (a carry past 2^80 moves the time on), so IDs sort in creation order;
  `job.NewID` and the request id in `internal/api/middleware.go` use it. Tests: four
  vectors the old library printed for the same time and random part
  (`TestEncodeULID`), format, time and uniqueness and order across 8 goroutines × 2,000
  under `-race` (`TestNewULID`), the carry (`TestIncrement`).
- **YAML.** Switched: an import change in 10 files (3 production), `go.yaml.in/yaml/v3
  v3.0.5`. `go mod tidy` also dropped `kr/text` and `rogpeppe/go-internal` (test deps of
  the old yaml module). All config, eval, scenario and conformance tests pass unchanged;
  `make eval` reports are identical to the 06-03 ones except timings.
- **Overview.** Rows added for PostgreSQL (pgx), S3 and Bedrock (aws-sdk-go-v2,
  smithy-go), Vertex AI auth (x/oauth2), Images (x/image); the YAML and IDs rows
  rewritten. The table now names exactly the 16 direct requirements of `go.mod`.
  `docs/security.md`'s licence list follows.
- `go mod tidy` leaves no diff. `make ci` green; its `govulncheck` (the `go.mod`
  toolchain) finds no vulnerability. With the local go1.27.1 only the known
  standard-library row of `docs/security.md` shows, no third-party finding.
  Binary (darwin/arm64, unstripped) −18 KB.

Coverage: `internal/job` 80.2 % → 80.8 %, `internal/api` 91.3 % → 91.3 %.

`git diff --stat`: production 6 files (+69 −8, of which `ulid.go` +63); tests 8 files
(+91 −7, of which `ulid_test.go` +84); `go.mod`/`go.sum` (+1 −18); docs 3 files.

## Out of scope

- Hand-written AWS, S3 or OAuth clients (high risk, unverified live).

# Job: Dependency hygiene and a local ULID

Version: v1.1 · Epic: 07 Dependencies and build · Status: planned · Depends on: 00-04

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

- [ ] Inventory.
  - Skill: GODEPS.
  - Prompt: "/golang-dependency-management For each direct require in go.mod: `go mod
    why -m`, importing packages, approximate binary weight, maintenance status (last
    release), and whether docs/architecture/overview.md 'Implementation decisions' has a
    row. Check whether gopkg.in/yaml.v3 is still maintained and what replaces it. Report
    only; change nothing."
- [ ] Replace ULID.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: replace oklog/ulid/v2 with a local
    newULID in internal/job (exported for internal/api's request id): same 26-character
    Crockford base32, millisecond time, crypto/rand, monotonic within a millisecond, with
    a test for format, ordering and uniqueness under concurrency; `go mod tidy`.
    Guardrails: skills.md (this removes a dependency: record it in the overview table)."
- [ ] Align the overview's dependency table with `go.mod`.
  - Skill: none (Edit).
  - Prompt: "Add rows to docs/architecture/overview.md 'Implementation decisions' for
    every direct dependency that lacks one (purpose, ADR or job that brought it), remove
    the ULID row, and the yaml row if it was switched. Targeted edits only."
- [ ] Gate.
  - Skill: REVIEW, GODEPS.
  - Prompt: `/code-review high <first>^..<last>`, then
    "/golang-dependency-management run govulncheck ./... and report."

## Acceptance criteria

- `go mod tidy` leaves no diff; the overview table and `go.mod` list the same direct
  dependencies; `govulncheck` clean.

## Out of scope

- Hand-written AWS, S3 or OAuth clients (high risk, unverified live).

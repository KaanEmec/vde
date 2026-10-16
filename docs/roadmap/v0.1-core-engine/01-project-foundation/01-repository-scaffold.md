# Job: Repository scaffold

Version: v0.1 · Epic: 01 Project foundation · Status: done · Depends on: none

## Goal

A Go module with the package layout from the architecture overview, a `vde` binary that
prints its version, a Makefile with `build`, `test`, `lint` and `check` targets, and the
repository files an open-source project needs on day one.

## Context

Architecture overview, "Packages". ADR 0001. Decision 1 (Apache-2.0; module path still open,
licence). The contract (§12) wants one binary offering `serve`, `token` and `config check`;
subcommands are added by later jobs, this job creates the command skeleton.

## Tasks

- [x] Initialize the Go module (Go 1.23+), `cmd/vde/main.go` with a subcommand dispatcher
      using the standard `flag` package (no CLI framework); `vde version` prints version,
      commit and build date injected with `-ldflags`.
- [x] Create the `internal/` package directories with a `doc.go` each stating the package's
      responsibility in two or three sentences.
- [x] Add `Makefile` targets: `build` (cgo disabled), `test` (`go test -race ./...`),
      `lint` (`go vet`, `staticcheck`, `golangci-lint` with a short config), `check`
      (all of the above), `cross` (linux/amd64 and linux/arm64 builds).
- [x] Add `LICENSE` (Apache-2.0 assumed), `README.md` (what it is, status, link to docs),
      `CONTRIBUTING.md` (job-per-PR working agreement from the roadmap README),
      `.gitignore`, `.editorconfig`.
- [x] Add `docs/` links to architecture and roadmap from the README.
- [x] Add an `examples/` folder placeholder for the dev configuration (filled by epic 06).
- [x] Import the co-owned TravelInbox assets into `testdata/travelinbox/`: the extraction
      schema, the extractor and judge prompts, the reference stub for study, and the
      evaluation corpus with ground truth (from the TravelInbox repository,
      `backend/testdata/corpus/` and `backend/internal/extract/`), with a README noting
      origin and date. Decision 11 in `docs/roadmap/open-questions.md`.

## Acceptance criteria

- `make check` passes on a clean checkout.
- `make cross` produces static binaries for both Linux architectures without cgo.
- `vde version` and `vde help` work; unknown subcommands exit non-zero with usage.

## Out of scope

Any functional subcommand, Dockerfile (v0.2), release automation (v1.0).

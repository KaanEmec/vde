# Job: CI pipeline

Version: v0.1 · Epic: 01 Project foundation · Status: done · Depends on: 01

## Goal

Every pull request runs the same checks as `make check`, plus vulnerability scanning and
cross-compilation, in a few minutes.

## Context

ADR 0001 (cgo-free builds). Release automation and image publishing are v0.2 epic 08 and
v1.0 epic 04; this job is only the PR gate.

## Tasks

- [x] GitHub Actions workflow on push and pull request: `go build`, `go test -race` with
      coverage artifact, `go vet`, `staticcheck`, `golangci-lint`, `govulncheck`,
      `make cross`. (`.github/workflows/ci.yml`; staticcheck runs inside golangci-lint,
      which enables it by default, so there is no separate step. The test job also checks
      `go mod tidy -diff` and runs on Go 1.26 and stable.)
- [x] Dependabot (or Renovate) for Go modules and Actions, weekly, grouped. (`.github/dependabot.yml`)
- [x] Cache the Go build and module cache. (`actions/setup-go` caching, keyed on `go.sum`)
- [x] Branch protection notes in `CONTRIBUTING.md`: CI green required, one review.

## Acceptance criteria

- A pull request that breaks a test, vet, lint or vulnerability check fails CI.
- CI finishes in under five minutes on a cached run.

## Notes

The workflow has not run on GitHub yet (no remote); each step was checked locally (`make cross`,
`go mod tidy -diff`, golangci-lint 2.14). The action major versions and `golangci-lint`
v2.14 are pinned in the file; Dependabot keeps the actions current.

## Out of scope

Integration tests against real providers (opt-in, v0.1 epic 06), container builds (v0.2).

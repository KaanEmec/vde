# Job: Baseline measurements and reference reports

Version: v1.1 · Epic: 00 Preparation · Status: done · Depends on: 00-03 (local CI merged into `main`)

## Goal

Freeze the numbers and reports that prove later jobs changed structure, not behaviour.

## Context

The version README holds the audit's numbers as of `78b6486`. Later jobs compare `make
eval` reports (`--compare`) and conformance results with this job's output. Reports stay
local (`eval-reports/` is gitignored); only numbers go into the version README.

## Tasks

- [ ] Re-measure the baseline table in the [version README](../README.md#baseline-2026-10-19-commit-78b6486)
      and correct it if the tree moved.
  - Skill: EVID.
  - Prompt: "In the repository root, measure: production and test Go lines (`git ls-files
    '*.go'`, split on `_test.go`), tracked files per top-level folder, direct
    dependencies in go.mod, a stripped linux/amd64 build size (`CGO_ENABLED=0 GOOS=linux
    GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o \"$TMPDIR/vde\" ./cmd/vde`),
    `deadcode ./cmd/...` count, `deadcode -test ./...` count, and the 15 longest
    production functions (go/ast script in the scratchpad). Never build to bin/vde.
    Report a table; change nothing in the repository."
- [ ] Record the one-off lint audit as the inventory for epics 01 to 05.
  - Skill: GOLINT.
  - Prompt: "/golang-lint Run a one-off audit without touching .golangci.yml:
    `golangci-lint run --no-config --default=none --enable=unused,unparam,dupl,gocognit,iface,ireturn,interfacebloat,wastedassign ./...`.
    Summarize findings per package with file:line, production code first. Do not fix
    anything. Guardrails: skills.md."
- [ ] Produce the reference reports.
  - Skill: none (make).
  - Prompt: "Run `make eval EVAL_OUT=eval-reports/lean-baseline` and `make conformance`
    and `make scenarios`; report pass/fail and the report file names. Print no case
    content."
- [ ] Time the test suite (`go test -race ./...` wall time) for the closeout comparison.
  - Skill: none.
  - Prompt: "Time `go test -race -count=1 ./...` once; report the wall time only."

## Acceptance criteria

- The version README's baseline table matches the measurements; three reference reports
  exist under `eval-reports/lean-baseline/`.

## Out of scope

- Any code or documentation change besides the baseline table.

# Job: CI, Makefile and Dockerfile

Version: v1.1 · Epic: 07 Dependencies and build · Status: planned · Depends on: 00-02

## Goal

CI runs every test once where once is enough, runs the Postgres tests it skips today, and
the Makefile, Dockerfile and toolchain versions agree.

## Context

- Coverage gap: the CI `storage` job runs `./internal/job/... ./internal/blob/...
  ./cmd/vde/...`; `internal/api/scaling_test.go` (`TestSharedRateLimitPostgres`) and
  `internal/worker/scaling_test.go` are gated on `VDE_TEST_POSTGRES` and never run in CI.
  The same job reruns the whole `cmd/vde` race suite only to reach `storage_test.go`.
  Fix: `go test -race -count=1 -run 'Postgres|S3|Storage|Scaling|Shared' ./...` with the
  environment set (check the names match).
- `lint` runs `go vet ./...` and then golangci-lint, whose standard set includes govet
  (CI and Makefile).
- `cross` could fold into `conformance`, which builds the binary anyway (keep the
  static-link check).
- `test` runs the race suite on 1.26 and stable; keep, or move stable to a weekly
  schedule (owner's call, default keep).
- `release.yml` `check` reruns `make check` on a tag CI already passed; optional gate on
  "CI succeeded for this commit".
- Makefile: `scenarios` repeats `TestScenarios` verbosely (named in four docs: keep as an
  alias or drop with the docs); `image-multiarch` is unused by CI.
- Toolchain: `Dockerfile` `ARG GO_VERSION=1.27`, `go.mod` `toolchain go1.26.9`, CI
  `"1.26"`, overview "1.27 toolchain": align on one.
- `.dockerignore` header comment omits `api/`, which is now copied.

## Tasks

- [ ] Trim and fix CI.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify .github/workflows/ci.yml .github/workflows/release.yml — make the
    storage job run only the Postgres, S3 and scaling tests across all packages (it must
    now include internal/api and internal/worker scaling tests); drop the separate go vet
    step (golangci-lint runs govet); fold cross into conformance keeping the static-link
    check. Do not add jobs, scanners or actions. Guardrails: skills.md."
- [ ] Makefile and toolchain.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify Makefile — drop `go vet` from lint, drop image-multiarch if
    nothing references it, keep `scenarios` as a one-line alias; align the Go version in
    Dockerfile, go.mod toolchain, ci.yml and docs/architecture/overview.md on the
    toolchain directive. Guardrails: skills.md."
- [ ] Dockerfile review.
  - Skill: DOCKER.
  - Prompt: "/multi-stage-dockerfile Review the existing Dockerfile and .dockerignore
    only for redundant stages, layers and stale comments (the header that omits api/);
    the image must stay static, CGO_ENABLED=0, non-root, with NOTICE and LICENSE. Report
    first, then apply only removals."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`; push to a branch and confirm every
    CI job is green before merging (the owner pushes).

## Acceptance criteria

- The CI `storage` job runs `TestSharedRateLimitPostgres` and the worker scaling test;
  total CI minutes per push lower than before; `make image` builds.

## Out of scope

- Adding Dependabot, CodeQL, coverage upload or any new job.

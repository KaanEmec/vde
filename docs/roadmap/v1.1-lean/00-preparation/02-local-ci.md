# Job: Local CI with `make ci`

Version: v1.1 · Epic: 00 Preparation · Status: planned · Depends on: 00-01; owner decision 18 ([open questions](../../open-questions.md#decided))

## Goal

Every check that GitHub Actions runs today runs on the owner's machine with one command,
`make ci`; `.github/workflows/ci.yml` and `.github/dependabot.yml` are deleted. GitHub
only stores the repository (and, after 00-03, the release artifacts). The local suite
also closes the coverage gap the audit found, and the Makefile, Dockerfile and toolchain
versions agree.

## Context

- Order: this job and 00-03 come first in v1.1, on one branch `v1.1/local-ci` cut
  from `main`; 00-03 ends by merging it into `main` and pushing. No other v1.1 job
  starts before that, so each one is checked with `make ci`.
- Owner's direction (2026-10-19, decision 18): CI and builds run locally; GitHub is an
  online backup where the builds also live.
- `ci.yml` jobs today, and their local equivalent in `make ci`:

  | CI job | Local step |
  | --- | --- |
  | `test` (Go 1.26 and stable): `go mod tidy -diff`, build, `go test -race -shuffle=on` with coverage | `go mod tidy -diff`, then the race suite twice: with `GOTOOLCHAIN` set to the `toolchain` line of `go.mod` (go1.26.9) and with the local Go (1.27.1 on 2026-10-19); this replaces the matrix |
  | `lint`: `go vet`, then golangci-lint v2.14 | golangci-lint only (its standard set runs govet) |
  | `vulncheck` | `govulncheck ./...` (installed) |
  | `fuzz`: seven targets for 20 s each | The same loop as a Makefile target `fuzz` |
  | `cross`: `make cross` and a static-link check | `make cross` plus `file … \| grep 'statically linked'`, building to `$(TMPDIR)` or `dist/`, never `bin/vde` (AGENTS.md) |
  | `conformance` | `make conformance` |
  | `eval` | `make eval EVAL_OUT=$(TMPDIR)/eval-reports` |
  | `storage`: PostgreSQL 17 and RustFS service containers; runs only job, blob and cmd/vde | A `storage` target that starts both with two `docker run` lines (the images and settings of today's `ci.yml`), waits for them, runs `go test -race -count=1 -run 'Postgres\|S3\|Storage\|Scaling\|Shared' ./...` with the `VDE_TEST_*` variables, and stops them. This now includes `internal/api` and `internal/worker` scaling tests (`TestSharedRateLimitPostgres`), which CI never ran |

- Dependabot opens pull requests on GitHub (weekly, Go modules and Actions). Locally:
  `govulncheck` in `make ci` for security, and `go list -m -u all` as a `deps` target
  run by hand before a release for updates.
- Docker 29.5.2 is installed; the storage step needs it (and is skipped with a clear
  message when Docker is not running, never silently passed).
- Makefile: `scenarios` repeats `TestScenarios` verbosely (named in four docs: keep as a
  one-line alias or drop with the docs); `image-multiarch` moves to 00-03.
- Toolchain: `Dockerfile` `ARG GO_VERSION=1.27`, `go.mod` `toolchain go1.26.9`, the old
  CI `"1.26"`, overview "1.27 toolchain": align on the `go.mod` line.
- `.dockerignore` header comment omits `api/`, which is now copied.
- `main` has no branch protection and no required checks (checked 2026-10-19), so
  removing the workflows blocks nothing on GitHub.
- A versioned pre-push hook (`git config core.hooksPath .githooks`, one file running
  `make check`) keeps the fast checks automatic; `make ci` stays manual and is required
  by `make release` (00-03).

## Tasks

- [ ] Write `make ci` and its targets (`fuzz`, `storage`, `deps`), replacing every
      `ci.yml` job as in the table.
  - Skill: none (Edit; no vetted skill writes Makefiles), DOCKER for the containers.
  - Prompt: "In the Makefile, add `ci` that runs, in order and stopping at the first
    failure: `go mod tidy -diff`; lint (golangci-lint only); the race suite with
    GOTOOLCHAIN set from the go.mod toolchain line, then with the local Go; govulncheck;
    fuzz (the seven targets of .github/workflows/ci.yml, 20 s each); cross with the
    static-link check into $(TMPDIR); conformance; eval into $(TMPDIR); storage (start
    postgres:17-alpine and rustfs/rustfs:1.0.1 with the settings of ci.yml via docker
    run, wait, run the Postgres/S3/scaling tests across ./..., stop the containers even
    on failure). Add `deps` (`go list -m -u all`). Never build to bin/vde. Print a
    one-line summary per step with its duration. Guardrails:
    docs/roadmap/v1.1-lean/skills.md."
- [ ] Prove parity, then delete the GitHub configuration.
  - Skill: none (make).
  - Prompt: "Run `make ci` from a clean checkout; confirm each ci.yml job has a step that
    ran and passed, and that TestSharedRateLimitPostgres and the worker scaling test ran
    (`-v` grep). Then delete .github/workflows/ci.yml and .github/dependabot.yml (and
    .github/ if empty after 00-03). Report durations."
- [ ] Pre-push hook.
  - Skill: none (Edit).
  - Prompt: "Add .githooks/pre-push running `make check` (exit non-zero on failure) and
    a `hooks` Makefile target that runs `git config core.hooksPath .githooks`; document
    both in CONTRIBUTING.md."
- [ ] Makefile tidy and toolchain alignment.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify Makefile — drop `go vet` from lint (golangci-lint runs govet),
    keep `scenarios` as a one-line alias, and align the Go version in Dockerfile and
    docs/architecture/overview.md on the go.mod toolchain line. Guardrails: skills.md."
- [ ] Dockerfile review.
  - Skill: DOCKER.
  - Prompt: "/multi-stage-dockerfile Review the existing Dockerfile and .dockerignore
    only for redundant stages, layers and stale comments (the header that omits api/);
    the image must stay static, CGO_ENABLED=0, non-root, with NOTICE and LICENSE. Report
    first, then apply only removals."
- [ ] Docs: every mention of GitHub CI becomes `make ci`.
  - Skill: none (Edit).
  - Prompt: "Replace references to GitHub Actions CI and ci.yml with `make ci` in
    AGENTS.md (repository map row `.github/workflows/`, Build table), CONTRIBUTING.md
    (required checks), README.md, STATUS.md, docs/security.md (fuzzers 'run in the CI
    fuzz job'), docs/quality.md, docs/operator/evaluation.md,
    docs/conformance/travelinbox-v1.md. Done roadmap versions keep their history. Run
    `make check`."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`, then `make ci` once more.

## Acceptance criteria

- `make ci` passes locally and covers every former `ci.yml` job plus the api and worker
  Postgres tests; `.github/workflows/ci.yml` and `.github/dependabot.yml` are gone; no
  document says a check runs on GitHub.

## Out of scope

- Releases (00-03); a self-hosted runner or another CI service.

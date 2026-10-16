# Contributing

## Working agreement

Work is planned in `docs/roadmap/` as versions, epics and jobs. One job is one unit of
change:

- Each job becomes one branch and one pull request, named after the job file
  (`v0.1/01-project-foundation/02-configuration-model`). Until a remote repository exists,
  each job lands as one commit on `main` with that name as the message.
- A job is `done` when its acceptance criteria are met by automated tests where tests are
  possible. Update the job file's `Status` and the epic README's table in the same change.
- A job that changes an architectural decision adds or amends an ADR in
  `docs/architecture/decisions/` in the same change.
- `make check` must pass before a commit.

## Code

- Standard library first. The allowed third-party dependencies are listed in the
  architecture overview, "Implementation decisions"; anything else needs an ADR.
- An interface exists only where a second implementation is planned.
- Tests use the standard `testing` package only, table-driven with named subtests. The
  fake provider is the backbone of tests; real provider calls run only in an opt-in suite.
- Document content, instructions, schemas, candidates, feedback and secrets never appear
  in logs, errors or test output.

## Continuous integration and branch protection

`.github/workflows/ci.yml` runs on every push to `main` and every pull request: `go mod
tidy` check, build, `go test -race` (with a coverage artifact), `go vet`, golangci-lint
(which includes staticcheck), `govulncheck` and a static linux/amd64 and linux/arm64
cross-build. Dependabot opens grouped weekly updates for Go modules and Actions.

Protect `main` with these settings once a remote exists:

- require a pull request with one approving review before merging;
- require the status checks `test (go 1.26)`, `test (go stable)`, `lint`, `vulncheck` and
  `cross-compile` to pass, with the branch up to date;
- no force pushes to `main`.

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
- Before tagging a release, run `make live` with at least one provider key set
  (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY` or `GEMINI_API_KEY`; the
  model per provider can be changed with `VDE_LIVE_<PROVIDER>_MODEL`). It runs the sample
  documents, and your own in `testdata/user-docs/` when present, through `vde extract`
  against the real provider and prints the token usage of each run to stderr.

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

## Releasing

A release is a `vX.Y.Z` tag (or a pre-release such as `v0.2.0-rc.1`) on `main`.
`.github/workflows/release.yml` does the rest: it runs `make check`, builds and pushes the
multi-arch image (`linux/amd64`, `linux/arm64`) to `ghcr.io/kaanemec/vde`, smoke-tests both
platforms with `vde version`, then lets GoReleaser (`.goreleaser.yaml`) build the static
binaries, archives and `checksums.txt` and create the GitHub release.

Image tags: `X.Y.Z` always; `X.Y` and `latest` only for stable versions, so a pre-release
never moves them. A tag with a suffix (`-rc.1`) becomes a GitHub pre-release.

Procedure:

1. `main` is green in CI and `make check` passes locally.
2. Run `make live` with at least one provider key set (see above) and read the output: the
   sample documents must extract through the real provider, and token usage must look sane.
   Do not tag if it fails.
3. In `CHANGELOG.md`, rename `Unreleased` to the version and date, start a fresh
   `Unreleased` section above it, and commit.
4. Optional dry run: `make image` and `docker run --rm vde version`, and
   `goreleaser release --snapshot --clean --skip=publish` (GoReleaser v2) to check the
   archives locally.
5. Tag and push: `git tag -a v0.2.0 -m v0.2.0 && git push origin v0.2.0`. Watch the
   `release` workflow.
6. Verify: `docker run --rm ghcr.io/kaanemec/vde:0.2.0 version` on amd64 and arm64, and
   that the GitHub release lists four archives and `checksums.txt`.

First release only: GHCR creates the package private. After the first push, open the
package settings on GitHub and make it public, and link it to the repository, so that
consumers can pull without credentials. Signing and SBOMs are v1.0 work.

For coding agents: [AGENTS.md](AGENTS.md) says how to work in this repository and [STATUS.md](STATUS.md) where the project stands.

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

## Checks

Every check runs on your machine; GitHub only stores the repository and the release
artifacts, and runs nothing.

- `make check` (golangci-lint, which includes govet and staticcheck; `go test -race`;
  build) passes before every commit. `make hooks`, once per clone, points git at
  `.githooks/`, whose `pre-push` runs `make check`.
- `make ci` runs everything, in order, stops at the first failure and prints one line per
  step with its duration: `go mod tidy -diff`; golangci-lint; the race suite
  (`-shuffle=on`) with the `toolchain` of `go.mod` and again with the local Go;
  `govulncheck` with the `go.mod` toolchain, which builds images and release binaries (on
  `PATH`: `go install golang.org/x/vuln/cmd/govulncheck@latest`);
  `make fuzz` (every fuzz target for `FUZZTIME`, 20 s); the static linux/amd64 and
  linux/arm64 cross-build; `make conformance`; `make eval`; and `make storage`, which
  starts PostgreSQL 17 and RustFS in throwaway containers and runs the job, blob and worker
  suites and the Postgres and S3 tests of `cmd/vde` and `internal/api`. Docker must be
  running: without it the storage step fails, it never passes silently. Run `make ci`
  before merging into `main`; `make release` runs it too.
- `make deps` lists available module updates; run it by hand before a release
  (`govulncheck` in `make ci` covers known vulnerabilities).

## Releasing

A release is a `vX.Y.Z` tag (or a pre-release such as `v1.1.0-rc.1`) on `main`, made on
the owner's machine with `make release VERSION=vX.Y.Z`. GitHub hosts the result and runs
nothing. The target, in order:

1. refuses unless `VERSION` is given, the tree is clean, the branch is `main`, `CHANGELOG.md`
   has a `## [X.Y.Z]` section and the tag exists neither locally nor on `origin`;
2. runs `make ci`;
3. `make dist`: builds linux and darwin, amd64 and arm64, with the `go.mod` toolchain
   (`GOTOOLCHAIN`), `-trimpath` and the version ldflags; packs
   `dist/vde_X.Y.Z_<os>_<arch>.tar.gz` (binary, `LICENSE`, `README.md`, `CHANGELOG.md`, the
   tracked `examples/`) and `dist/checksums.txt` (SHA-256); verifies the checksums, the
   toolchain, static linking and `vde version` (the linux binaries in Docker); writes the
   CHANGELOG section to `dist/notes.md`;
4. `make image-release`: builds `ghcr.io/kaanemec/vde` for `linux/amd64` and `linux/arm64`
   with buildx, pushes it, and runs `vde version` on both platforms. Image tags: `X.Y.Z`
   always; `X.Y` and `latest` only for stable versions, so a pre-release never moves them;
5. `make publish`: pushes an annotated tag and runs `gh release create` with the archives,
   `checksums.txt` and the notes (`--prerelease` for a suffixed version).

Prerequisites, once: Docker running with the buildx plugin (`brew install docker-buildx`;
`docker buildx ls` shows `linux/amd64` and `linux/arm64`); `gh` logged in with
`write:packages` (`gh auth refresh -s write:packages`) and Docker logged in to `ghcr.io`
with that token; `govulncheck` and golangci-lint on `PATH`. No token is written to the
Makefile, `dist/` or the output.

Procedure:

1. `make deps` for available updates; `make live` with at least one provider key set (see
   above): the sample documents must extract through the real provider, and token usage
   must look sane. Do not release if it fails.
2. In `CHANGELOG.md`, rename `Unreleased` to the version and date, start a fresh
   `Unreleased` section above it, and commit on `main`.
3. Dry run: `make release VERSION=vX.Y.Z DRY_RUN=1` does everything except pushing the
   image, the tag and the release (the image is loaded into the local Docker instead). It
   also runs on a branch other than `main`, warns instead of failing when the CHANGELOG
   section is missing, and skips the image with a warning when buildx is not installed.
4. `make release VERSION=vX.Y.Z`.
5. Verify: `docker run --rm ghcr.io/kaanemec/vde:X.Y.Z version` on amd64 and arm64, and
   that the GitHub release lists four archives and `checksums.txt`.

First release to GHCR only: the package is created private. After the first push, open the
package settings on GitHub, make it public and link it to the repository, so that
consumers can pull without credentials. Signing and SBOMs are deferred (backlog).

For coding agents: [AGENTS.md](AGENTS.md) says how to work in this repository and [STATUS.md](STATUS.md) where the project stands.

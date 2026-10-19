# Contributing

## Working agreement

Work is planned in `docs/roadmap/` as versions, epics and jobs; one job lands as one commit
on `main`. The conventions (commit messages, statuses, ADRs, tests) are in
[AGENTS.md, "Conventions"](AGENTS.md#conventions); the design principles in the
[architecture overview](docs/architecture/overview.md#design-principles).

## Checks

Every check runs on your machine; GitHub only stores the repository and the release
artifacts, and runs nothing. The commands are listed in
[AGENTS.md, "Build, test, validate"](AGENTS.md#build-test-validate).

- `make check` passes before every commit. `make hooks`, once per clone, points git at
  `.githooks/`, whose `pre-push` runs `make check`.
- `make ci` before merging into `main` (`make release` runs it too). It stops at the first
  failure and prints one line per step with its duration. It needs `govulncheck` and
  golangci-lint on `PATH` (`go install golang.org/x/vuln/cmd/govulncheck@latest`) and a
  running Docker: its storage step starts PostgreSQL 17 and RustFS in throwaway containers
  and fails, never passes silently, without Docker.
- `make deps` lists available module updates; run it by hand before a release.

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

1. `make deps` for available updates; `make live` with at least one provider key set
   ([AGENTS.md](AGENTS.md#build-test-validate)): the sample documents must extract through
   the real provider, and token usage must look sane. Do not release if it fails.
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

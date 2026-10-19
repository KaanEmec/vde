# Job: Local release to GitHub Releases and GHCR

Version: v1.1 · Epic: 00 Preparation · Status: planned · Depends on: 00-02; owner decision 18 ([open questions](../../open-questions.md#decided))

## Goal

`make release VERSION=vX.Y.Z` on the owner's machine does what `release.yml` does on
GitHub today: run every check, build the binaries and the multi-arch image, and publish
them. GitHub keeps the artifacts (a GitHub release with archives and checksums, the image
on GHCR) but runs nothing. `.github/workflows/release.yml` is deleted and GitHub Actions
is turned off for the repository.

## Context

- `release.yml` today: on a `v*` tag, runs `make check`; builds and pushes
  `ghcr.io/kaanemec/vde` for linux/amd64 and linux/arm64 with buildx (tags `X.Y.Z`
  always, `X.Y` and `latest` only for a stable version); smoke-tests both platforms with
  `vde version`; then GoReleaser (`.goreleaser.yaml`) builds linux and darwin binaries for
  amd64 and arm64, four `tar.gz` archives (with LICENSE, README, CHANGELOG, `examples/`),
  `checksums.txt`, and creates the GitHub release (pre-release for a suffixed tag).
- Leaner option, the default: the Makefile builds the four archives and `checksums.txt`
  into `dist/` (gitignored) and `gh release create` uploads them with the version's
  CHANGELOG section as notes. That removes `.goreleaser.yaml` (about 90 lines) and the
  need to install GoReleaser (not installed on 2026-10-19). Alternative: keep
  `.goreleaser.yaml` and run `goreleaser release --clean` locally with
  `GITHUB_TOKEN=$(gh auth token)`; decide in the first task.
- Release builds pin the toolchain: `GOTOOLCHAIN` from the `go.mod` toolchain line
  (go1.26.9), because the local Go is newer (1.27.1) and would otherwise build the
  release (the v1.0 review pinned the release toolchain for a security fix).
- The owner's machine on 2026-10-19: Docker 29.5.2 without the buildx plugin (`docker
  buildx` is unknown; `brew install docker-buildx` adds it); the host is arm64, so the
  amd64 smoke test needs the Docker VM's emulation. `gh` is logged in with scopes
  `repo` and `workflow`; pushing to GHCR needs `write:packages`
  (`gh auth refresh -s write:packages`, then `docker login ghcr.io` with that token).
- CONTRIBUTING.md "Releasing" describes the GitHub flow and must be rewritten;
  `make live` before a release stays (owner's endpoint).
- Turning Actions off is a repository setting: the owner does it in the repository's
  settings (Actions, "Disable actions") or with `gh api`.

## Tasks

- [ ] Choose GoReleaser or Makefile plus `gh` for the archives.
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens): compare keeping
    .goreleaser.yaml run locally against Makefile targets plus `gh release create` for
    four static archives and checksums.txt: lines to maintain, tools to install,
    reproducibility, what the release page loses (GoReleaser's commit-grouped notes vs
    the CHANGELOG section). Recommend one."
- [ ] Owner prepares the machine (credentials and plugins are the owner's to set).
  - Skill: none.
  - Prompt: none. Steps: install the buildx plugin; check `docker buildx ls` shows
    linux/amd64 and linux/arm64; `gh auth refresh -s write:packages`; log Docker in to
    `ghcr.io` with that token.
- [ ] Write `make release` and `make image-release`.
  - Skill: none (Edit), DOCKER for the image step.
  - Prompt: "In the Makefile add `release` (needs VERSION=vX.Y.Z): refuse unless on main
    with a clean tree, CHANGELOG.md has a section for the version, and the tag does not
    exist locally or on origin; run `make ci`; build linux and darwin, amd64 and arm64,
    with GOTOOLCHAIN pinned to the go.mod toolchain line, -trimpath and the ldflags of
    the Makefile, into dist/; pack vde_<version>_<os>_<arch>.tar.gz with LICENSE,
    README.md, CHANGELOG.md and examples/; write dist/checksums.txt (sha256); then
    `image-release`: buildx build for linux/amd64,linux/arm64 with VERSION, COMMIT and
    DATE build args, push tags X.Y.Z (always), X.Y and latest (stable only), and run
    `vde version` on both platforms; then create and push an annotated tag and run
    `gh release create` with the archives, checksums.txt, the CHANGELOG section as notes
    and --prerelease for a suffixed version. With DRY_RUN=1, do everything except
    pushing the image, the tag and the release. Replace image-multiarch. Guardrails:
    docs/roadmap/v1.1-lean/skills.md."
- [ ] Dry run.
  - Skill: none (make).
  - Prompt: "Run `make release VERSION=v1.1.0-rc.0 DRY_RUN=1`; list dist/, verify
    checksums.txt with `shasum -a 256 -c`, check each linux binary is static and prints
    the version, and that both image platforms ran `vde version`. Push nothing."
- [ ] Delete the release workflow and update the docs.
  - Skill: none (Edit).
  - Prompt: "Delete .github/workflows/release.yml (and .goreleaser.yaml if the Makefile
    route was chosen; then .github/ if empty). Rewrite CONTRIBUTING.md 'Releasing' for
    `make release` (prerequisites, `make live` first, dry run, the GHCR visibility note
    for a first push), and update AGENTS.md (repository map, Build table), README.md and
    docs/operator/install.md wherever they say a release is built on GitHub. Run `make
    check`."
- [ ] Gate for 00-02 and 00-03 together.
  - Skill: REVIEW, SEC.
  - Prompt: `/code-review medium main..v1.1/local-ci`, then `/security-review` (release
    credentials and published artifacts: no token in the Makefile, logs or dist/).
- [ ] Merge into `main` and push (the milestone of epic 00).
  - Skill: none (git).
  - Prompt: "On v1.1/local-ci run `make ci`; then `git switch main && git merge --no-ff
    v1.1/local-ci -m 'v1.1/00-preparation: local CI and release'`, run `make ci` on
    main, and stop: the owner pushes main to origin."
- [ ] Owner pushes `main` and turns GitHub Actions off for the repository.
  - Skill: none.
  - Prompt: none (a push and a repository setting; the owner does both).

## Acceptance criteria

- `make release … DRY_RUN=1` produces four archives, `checksums.txt` and a two-platform
  image that answers `vde version`; no workflow file remains; Actions is disabled on the
  repository; CONTRIBUTING describes only the local release.
- `main` on origin contains the local CI and release, and no Actions run appears for
  that push.
- The first real use is `v1.1.0` in 10-01.

## Out of scope

- Signing and SBOMs (deferred from v1.0, backlog); a self-hosted runner.

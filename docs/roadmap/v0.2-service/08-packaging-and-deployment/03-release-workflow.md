# Job: Release workflow

Version: v0.2 · Epic: 08 Packaging and deployment · Status: done · Depends on: 01, v0.1 01-04

## Goal

Pushing a `vX.Y.Z` tag builds and publishes the multi-arch image to a public registry and
attaches static binaries and checksums to a GitHub release, with a changelog.

## Context

Contract §12 (semver-tagged public image). v1.0 epic 04 hardens this (signing, SBOM).

## Tasks

- [x] GoReleaser configuration: binaries for linux/darwin × amd64/arm64, archives,
      checksums, changelog from conventional commit prefixes or PR titles.
- [x] GitHub Actions release workflow on tags: run `make check`, build and push images
      to GHCR (`ghcr.io/<org>/vde:<semver>`, `:<major.minor>`, `:latest` only for
      stable), create the release.
- [x] `CHANGELOG.md` kept by hand in "Keep a Changelog" format; the release notes link
      to it.
- [x] Document the release procedure in `CONTRIBUTING.md`, including running the
      live-provider smoke test before tagging.

## Acceptance criteria

- A pre-release tag `v0.2.0-rc.1` publishes an image TravelInbox can pull.

## Implementation notes

- `.goreleaser.yaml` (checked with `goreleaser check`, and a snapshot release built the four
  binaries and archives locally), `.github/workflows/release.yml` (jobs `check`, `image`,
  `release`), `CHANGELOG.md`, and the "Releasing" section of `CONTRIBUTING.md`.
- The image is built by buildx in the workflow, not by GoReleaser, so one `Dockerfile` serves
  `make image` and releases. Tags: `X.Y.Z`; `X.Y` and `latest` for stable versions only. The
  GitHub release is created last, so a published release implies a pullable image.
- Not verified: the workflow itself (no remote, no actionlint available), so the
  `v0.2.0-rc.1` acceptance criterion is open until the first tag is pushed. The first push
  also needs the GHCR package made public (noted in `CONTRIBUTING.md`).

## Out of scope

Signing and SBOM (v1.0), Homebrew or package repositories.

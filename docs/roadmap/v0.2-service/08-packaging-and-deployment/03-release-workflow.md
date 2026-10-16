# Job: Release workflow

Version: v0.2 · Epic: 08 Packaging and deployment · Status: planned · Depends on: 01, v0.1 01-04

## Goal

Pushing a `vX.Y.Z` tag builds and publishes the multi-arch image to a public registry and
attaches static binaries and checksums to a GitHub release, with a changelog.

## Context

Contract §12 (semver-tagged public image). v1.0 epic 04 hardens this (signing, SBOM).

## Tasks

- [ ] GoReleaser configuration: binaries for linux/darwin × amd64/arm64, archives,
      checksums, changelog from conventional commit prefixes or PR titles.
- [ ] GitHub Actions release workflow on tags: run `make check`, build and push images
      to GHCR (`ghcr.io/<org>/vde:<semver>`, `:<major.minor>`, `:latest` only for
      stable), create the release.
- [ ] `CHANGELOG.md` kept by hand in "Keep a Changelog" format; the release notes link
      to it.
- [ ] Document the release procedure in `CONTRIBUTING.md`, including running the
      live-provider smoke test before tagging.

## Acceptance criteria

- A pre-release tag `v0.2.0-rc.1` publishes an image TravelInbox can pull.

## Out of scope

Signing and SBOM (v1.0), Homebrew or package repositories.

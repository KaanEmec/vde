# Job: Multi-arch container image

Version: v0.2 · Epic: 08 Packaging and deployment · Status: done · Depends on: 01-01

## Goal

A small, non-root image with `vde` as entrypoint, built for both Linux architectures from
one Dockerfile.

## Context

Contract §12: one image on a public registry, server as entrypoint, semver tags, same
binary offering `serve`, `token`, `config check`, MUST run on `linux/arm64` and
`linux/amd64`. ADR 0001 (cgo-free).

## Tasks

- [x] Multi-stage Dockerfile: build with the official Go image and `CGO_ENABLED=0`,
      run from `gcr.io/distroless/static` (or `scratch` plus CA certificates and
      tzdata); non-root user; `VOLUME /var/lib/vde`; `EXPOSE 8090`; `ENTRYPOINT
      ["vde"]`, `CMD ["serve", "--config", "/etc/vde/vde.yaml"]`.
- [x] `HEALTHCHECK` calling `vde healthcheck` (a tiny subcommand hitting `/healthz` on
      the configured listener, because distroless has no curl).
- [x] `docker buildx` for both platforms in a Makefile target; image labels with version
      and commit (OCI annotations).
- [x] Image size target under 40 MB.

## Acceptance criteria

- `docker run --rm image version` works on both platforms (CI uses QEMU for arm64).
- `docker run … config check` validates a mounted configuration.

## Implementation notes

- `Dockerfile`, `.dockerignore`, `make image` (local architecture) and `make image-multiarch`
  (buildx, `linux/amd64,linux/arm64`; `PUSH=1` pushes). The builder stage runs on the build
  platform and cross-compiles (`GOOS`/`GOARCH` from `TARGETOS`/`TARGETARCH`), so arm64 needs
  no emulation to build. The runtime image is `gcr.io/distroless/static:nonroot` (CA
  certificates and tzdata included), user 65532; `/var/lib/vde` is created owned by that user
  so a named volume is writable.
- Verified with a locally built image (linux/arm64): `docker run --rm image version` and
  `config check` on a mounted `examples/vde.dev.yaml` and `examples/compose/vde.yaml` exit 0.
  Size 19.3 MB on disk (4.6 MB compressed), well under the 40 MB target.
- Not verified: the Docker on the development machine has no buildx plugin, so the
  BuildKit-only parts of the `Dockerfile` (`--platform=$BUILDPLATFORM`, cache mounts) and
  `make image-multiarch` were not run; the local build used a copy without those two
  features. The linux/amd64 image and the QEMU arm64 smoke test run for the first time in
  the release workflow.
- `HEALTHCHECK` calls `vde healthcheck`, which does not exist yet: it is added by epic 02/07
  (the `serve` listener and `/healthz`, job 07-01). Until then a running container reports
  unhealthy; the check is already in the image so the contract does not change later.

## Out of scope

Helm chart, systemd unit (a sample unit file may go in `examples/`).

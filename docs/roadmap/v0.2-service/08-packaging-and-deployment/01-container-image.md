# Job: Multi-arch container image

Version: v0.2 · Epic: 08 Packaging and deployment · Status: planned · Depends on: 01-01

## Goal

A small, non-root image with `vde` as entrypoint, built for both Linux architectures from
one Dockerfile.

## Context

Contract §12: one image on a public registry, server as entrypoint, semver tags, same
binary offering `serve`, `token`, `config check`, MUST run on `linux/arm64` and
`linux/amd64`. ADR 0001 (cgo-free).

## Tasks

- [ ] Multi-stage Dockerfile: build with the official Go image and `CGO_ENABLED=0`,
      run from `gcr.io/distroless/static` (or `scratch` plus CA certificates and
      tzdata); non-root user; `VOLUME /var/lib/vde`; `EXPOSE 8090`; `ENTRYPOINT
      ["vde"]`, `CMD ["serve", "--config", "/etc/vde/vde.yaml"]`.
- [ ] `HEALTHCHECK` calling `vde healthcheck` (a tiny subcommand hitting `/healthz` on
      the configured listener, because distroless has no curl).
- [ ] `docker buildx` for both platforms in a Makefile target; image labels with version
      and commit (OCI annotations).
- [ ] Image size target under 40 MB.

## Acceptance criteria

- `docker run --rm image version` works on both platforms (CI uses QEMU for arm64).
- `docker run … config check` validates a mounted configuration.

## Out of scope

Helm chart, systemd unit (a sample unit file may go in `examples/`).

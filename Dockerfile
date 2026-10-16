# syntax=docker/dockerfile:1
# vde container image: static, cgo-free binary on distroless, non-root.
# Build:  docker build --build-arg VERSION=$(git describe --tags --always) -t vde .
# Multi-arch builds use `make image-multiarch`; the builder always runs on the build
# platform and cross-compiles, so no emulation is needed for arm64.

ARG GO_VERSION=1.27

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
WORKDIR /src

# Dependencies first, so source edits do not invalidate the module download layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/kaanemec/vde/internal/version.Version=${VERSION} \
        -X github.com/kaanemec/vde/internal/version.Commit=${COMMIT} \
        -X github.com/kaanemec/vde/internal/version.Date=${DATE}" \
      -o /out/vde ./cmd/vde \
    # The data directory must exist and belong to the runtime user (65532, "nonroot"),
    # because a named volume mounted there inherits that ownership.
    && mkdir -p /out/data/var/lib/vde

FROM gcr.io/distroless/static:nonroot
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
LABEL org.opencontainers.image.title="vde" \
      org.opencontainers.image.description="Verified Document Extraction service" \
      org.opencontainers.image.source="https://github.com/kaanemec/vde" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${DATE}"

COPY --from=builder /out/vde /usr/local/bin/vde
COPY --from=builder --chown=65532:65532 /out/data/var/lib/vde /var/lib/vde

USER nonroot:nonroot
VOLUME /var/lib/vde
EXPOSE 8090

# `vde healthcheck` requests /healthz on the configured listener (distroless has no curl).
# The subcommand ships with epic 02/07; until then the check reports unhealthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["vde", "healthcheck"]

ENTRYPOINT ["vde"]
CMD ["serve", "--config", "/etc/vde/vde.yaml"]

# Job: Image conversion

Version: v0.3 · Epic: 04 Image conversion · Status: done · Depends on: v0.1 04-01

## Goal

Accept `image/heic` and `image/webp` (contract SHOULD) even when no endpoint declares them,
by converting to JPEG in the service; downscale very large images to a configured maximum
dimension to save tokens.

## Tasks

- [x] WebP via `golang.org/x/image/webp`; HEIC via a pure-Go decoder if one is adequate,
      else document the limitation and keep HEIC "accepted only when an endpoint declares
      it".
- [x] `documents.images: {max_dimension: 2048, convert_to: image/jpeg, quality: 85}`;
      conversion result replaces the representation bytes for both roles (same bytes rule
      holds, conversion happens once).
- [x] EXIF orientation applied before sending.
- [x] Part of `service_version`; tests with fixtures.

## Deviations

- HEIC: no adequate pure-Go decoder exists and cgo is excluded (ADR 0001), so HEIC stays
  "accepted only when an endpoint declares it"; `image/heic` passes through untouched.
- `documents.images.enabled` (default `true`) was added to the keys; `convert_to` accepts
  `image/jpeg` or `image/png`, `max_dimension` is validated to 64-16384.
- Conversion runs in `document.SelectWith` (a new variant of `Select` taking a
  `document.ImagePolicy`; `Select` is unchanged and converts nothing), not in
  `document.New`: conversion needs the groups' capabilities to decide whether a WebP is
  converted. A PNG or JPEG within `max_dimension` and upright is never re-encoded; an
  oversize PNG becomes `convert_to`. If neither group pair reads the converted type, the
  image as given is tried next (the old behaviour).
- Alpha is composited over white for JPEG output. Images over 120 megapixels are refused
  (`413`); an undecodable PNG or JPEG is passed through as given.
- EXIF orientation: all eight values, parsed by hand from the APP1 segment.
- `service_version`: callers hash `ImagePolicy.String()` (`document.NewImagePolicy(cfg.Documents.Images)`).
- Wiring `SelectWith` into `cmd/vde` and `internal/api` and the hash into
  `internal/extract/version.go` belongs to those packages.

## Acceptance criteria

- A WebP document is processed by an endpoint that accepts only JPEG and PNG.

## Out of scope

Image enhancement (deskew, contrast).

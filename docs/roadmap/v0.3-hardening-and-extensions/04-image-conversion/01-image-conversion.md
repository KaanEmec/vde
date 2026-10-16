# Job: Image conversion

Version: v0.3 · Epic: 04 Image conversion · Status: in progress · Depends on: v0.1 04-01

## Goal

Accept `image/heic` and `image/webp` (contract SHOULD) even when no endpoint declares them,
by converting to JPEG in the service; downscale very large images to a configured maximum
dimension to save tokens.

## Tasks

- [ ] WebP via `golang.org/x/image/webp`; HEIC via a pure-Go decoder if one is adequate,
      else document the limitation and keep HEIC "accepted only when an endpoint declares
      it".
- [ ] `documents.images: {max_dimension: 2048, convert_to: image/jpeg, quality: 85}`;
      conversion result replaces the representation bytes for both roles (same bytes rule
      holds, conversion happens once).
- [ ] EXIF orientation applied before sending.
- [ ] Part of `service_version`; tests with fixtures.

## Acceptance criteria

- A WebP document is processed by an endpoint that accepts only JPEG and PNG.

## Out of scope

Image enhancement (deskew, contrast).

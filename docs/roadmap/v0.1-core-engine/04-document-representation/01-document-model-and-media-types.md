# Job: Document model and media types

Version: v0.1 · Epic: 04 Document representation · Status: done · Depends on: 01-02

## Goal

`internal/document`: a `Document` value (bytes, media type with parameters, optional page
images, optional filename) and the list of accepted media types with their category
(text, image, pdf) and size limits.

## Context

Contract §4: MUST accept `application/pdf`, `image/jpeg`, `image/png`, `text/html`,
`text/plain`; SHOULD accept `image/webp`, `image/heic`, `text/markdown`. Limits: document
25 MiB, page 10 MiB, 20 pages. The brief lists emails as input and decision 7 makes
`message/rfc822` a core type from v0.1 (job 04).

## Tasks

- [x] Media type parsing with `mime.ParseMediaType`; normalize (lowercase, keep `charset`
      for text). Table of accepted types → category and whether it is MUST or SHOULD.
- [x] Sniff the first bytes with `http.DetectContentType` and refuse a mismatch between
      declared and detected type for binary types (a PNG declared as PDF) with
      `unsupported_media_type`; text types are trusted but must be valid UTF-8 after
      charset handling (decode `charset` with `golang.org/x/text` when not UTF-8).
- [x] Pages: validate each is PNG or JPEG by sniffing, count and size limits from config
      with contract defaults; pages keep request order.
- [x] HEIC and WebP: accepted when some endpoint-model pair declares them; otherwise the
      representation selection falls back to pages or refuses. Conversion to JPEG is a
      v0.3 job (server-side rendering epic).
- [x] Limits are configurable (`limits.document_bytes`, `limits.page_bytes`,
      `limits.pages`) and default to the contract's numbers.

## Acceptance criteria

- Table tests for every accepted type, a rejected type, a mismatched sniff, a non-UTF-8
  text with a declared charset, and page validation.

## Out of scope

Reading PDF structure, rendering pages (never, ADR 0006). Email parsing is job 04.

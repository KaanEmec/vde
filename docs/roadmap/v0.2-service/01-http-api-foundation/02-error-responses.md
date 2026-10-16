# Job: Error responses and codes

Version: v0.2 · Epic: 01 HTTP API foundation · Status: planned · Depends on: 01

## Goal

One function writes every error: the contract's body shape, the right status, the right
headers, and a message that never contains content.

## Context

Contract §9: `{"error": {"code", "message", "request_id"}}`, `Content-Type:
application/json`, the HTTP code table, "Error messages MUST NOT quote document contents,
instructions or extraction output."

## Tasks

- [ ] `internal/api/errors.go`: an `apiError` type with status, code and message; a
      `writeError(w, r, apiError)` that adds `request_id`, keeps quota headers already set
      (epic 05), and `Retry-After` when provided.
- [ ] Constants for every contract code: `bad_request`, `unauthorized`, `forbidden`,
      `not_found`, `payload_too_large`, `unsupported_media_type`, `unsupported_document`,
      `invalid_schema`, `idempotency_mismatch`, `rate_limited`, `quota_exhausted`,
      `internal`, `provider_unavailable`, `unavailable`, `timeout`.
- [ ] Messages are fixed templates with at most a part name or a limit number
      interpolated; a lint-style test scans the api package for `writeError` calls whose
      message is not a constant or template.
- [ ] `404` for unknown routes and `405` for wrong methods also use the body shape.

## Acceptance criteria

- A test table maps every code to its status and asserts the body shape.

## Out of scope

Problem Details (RFC 9457): the contract fixed its own shape.

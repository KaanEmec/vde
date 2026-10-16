# Job: JSON submission with base64 or URL document

Version: v0.3 · Epic: 05 Delivery options · Status: done · Depends on: v0.2 04-01

## Goal

Make the API easier from environments where multipart is awkward: `POST /v1/jobs` with
`application/json` carrying base64 content, or a document URL the service fetches under
strict rules.

## Tasks

- [x] JSON body with the same fields as parts (`document: {media_type, data_base64}`,
      `pages`, `extractor_instructions`, `judge_instructions`, `schema` object,
      `retention`, `max_attempts`); decoded size limits identical; shared validation
      code with the multipart path.
- [x] URL fetch: off by default; per-application allow-list of hosts; HTTPS only;
      size and time limits; no redirects to other hosts; private address ranges refused
      (SSRF). Document the risk; keep the implementation small or drop URL fetch if the
      allow-list is not enough for a real consumer.
- [x] OpenAPI and docs.

## Outcome

`Content-Type: application/json` on `POST /v1/jobs` (and `POST /v1/extract`) decodes into
the same `submission` the multipart reader builds (`internal/api/submit_json.go`), so
options, document checks, schema compilation, representation, idempotency and admission are
the multipart code. Limits apply to decoded bytes; unknown fields are `400`; `schema` must
be an object; `max_attempts` a JSON integer. Tests: `submit_json_test.go` runs text, HTML,
PDF with pages, image and schema-less scenarios through both paths and compares the jobs,
plus refusals and idempotency.

URL fetch shipped (`internal/api/fetch.go`, about 110 lines): `documents.fetch: {enabled:
false, allowed_hosts, max_bytes, timeout}`; HTTPS only; exact host (or host:port)
allow-list; redirects only to the same host; the resolved address is checked in the
dialer's `Control` hook (loopback, private, link-local, multicast, unspecified, shared
100.64/10, NAT64 refused), so DNS rebinding cannot slip past; no environment proxy; size
and time caps. Tests use a TLS `httptest` server with loopback allowed and check that the
production address check refuses it.

Deviations:

- The allow-list is service-wide (`documents.fetch`), not per application.
- The fetch runs before the submission rate limit and quota (it is part of reading the
  body); only the request rate limit bounds it.

## Acceptance criteria

- The same scenario suite passes through the JSON path.

## Out of scope

Pre-signed upload URLs.

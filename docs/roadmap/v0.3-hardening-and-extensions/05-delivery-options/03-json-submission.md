# Job: JSON submission with base64 or URL document

Version: v0.3 · Epic: 05 Delivery options · Status: in progress · Depends on: v0.2 04-01

## Goal

Make the API easier from environments where multipart is awkward: `POST /v1/jobs` with
`application/json` carrying base64 content, or a document URL the service fetches under
strict rules.

## Tasks

- [ ] JSON body with the same fields as parts (`document: {media_type, data_base64}`,
      `pages`, `extractor_instructions`, `judge_instructions`, `schema` object,
      `retention`, `max_attempts`); decoded size limits identical; shared validation
      code with the multipart path.
- [ ] URL fetch: off by default; per-application allow-list of hosts; HTTPS only;
      size and time limits; no redirects to other hosts; private address ranges refused
      (SSRF). Document the risk; keep the implementation small or drop URL fetch if the
      allow-list is not enough for a real consumer.
- [ ] OpenAPI and docs.

## Acceptance criteria

- The same scenario suite passes through the JSON path.

## Out of scope

Pre-signed upload URLs.

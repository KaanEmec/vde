# Job: Multipart parsing, limits and submit handler

Version: v0.2 · Epic: 04 Job submission and retrieval · Status: planned · Depends on: 01-*, 02-03, 03-*

## Goal

Read the whole multipart request under configured limits, validate every part, compile
the schema, select the representation, store blobs and the job, charge the quota, answer
`202` with the job resource and `Location`.

## Context

Contract §4 in full: parts table, media types, limits, "MUST read the whole request before
answering so that the client sees the status rather than a reset connection", response
table (`400`, `413`, `415`, `422`). Contract §6 step 1.

## Tasks

- [ ] Body limit: `http.MaxBytesReader` at `limits.request_bytes` (128 MiB default) →
      `413` when exceeded; stream parts with `multipart.Reader`, enforcing per-part
      limits while reading (document 25 MiB, page 10 MiB, instructions 64 KiB, schema
      256 KiB, 20 pages). On a limit violation, drain the remaining body up to the
      request limit before answering so the client sees the status.
- [ ] Part rules: `document` exactly 1 with a media type (`400` when missing or
      repeated, `415 unsupported_media_type` when not accepted; `message/rfc822` is
      accepted and parsed per v0.1 04-04); `page` 0..20 PNG or
      JPEG; `extractor_instructions` and `judge_instructions` exactly 1, `text/plain` or
      `text/markdown`; `schema` 0..1 `application/schema+json` (optional: schema-less
      mode); `retention` 0..1 `none|default`; `max_attempts` 0..1 positive integer,
      clamped to the application's limit; unknown parts → `400`.
- [ ] Order of checks: parts and limits → media type → schema compile (`422
      invalid_schema`) → representation selection (`415 unsupported_document`) →
      idempotency (next job) → quota charge (epic 05) → store blobs and row → wake
      worker → `202` with `Location` and the job body (`status: queued`, `attempts: 0`).
- [ ] Nothing is written to the store or blob store before all validation passed; on a
      storage failure after blobs were written, delete them and answer `503
      unavailable`.
- [ ] Tests with real multipart bodies for every row of the response table, including
      an oversized body and an oversized single part, a PNG declared as PDF, a schema
      that is not JSON Schema, a PDF with no pages and no native-capable endpoint.

## Acceptance criteria

- A 130 MiB request receives a `413` JSON body, not a reset connection (test with a
  real TCP client).
- The response body matches the contract's example for a queued job.

## Out of scope

Synchronous extraction (v0.3), JSON (non-multipart) submission (v0.3 delivery options).

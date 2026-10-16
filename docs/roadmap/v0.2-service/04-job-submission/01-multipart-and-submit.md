# Job: Multipart parsing, limits and submit handler

Version: v0.2 · Epic: 04 Job submission and retrieval · Status: done · Depends on: 01-*, 02-03, 03-*

## Goal

Read the whole multipart request under configured limits, validate every part, compile
the schema, select the representation, store blobs and the job, charge the quota, answer
`202` with the job resource and `Location`.

## Context

Contract §4 in full: parts table, media types, limits, "MUST read the whole request before
answering so that the client sees the status rather than a reset connection", response
table (`400`, `413`, `415`, `422`). Contract §6 step 1.

## Tasks

- [x] Body limit: `http.MaxBytesReader` at `limits.request_bytes` (128 MiB default) →
      `413` when exceeded; stream parts with `multipart.Reader`, enforcing per-part
      limits while reading (document 25 MiB, page 10 MiB, instructions 64 KiB, schema
      256 KiB, 20 pages). On a limit violation, drain the remaining body up to the
      request limit before answering so the client sees the status.
- [x] Part rules: `document` exactly 1 with a media type (`400` when missing or
      repeated, `415 unsupported_media_type` when not accepted; `message/rfc822` is
      accepted and parsed per v0.1 04-04); `page` 0..20 PNG or
      JPEG; `extractor_instructions` and `judge_instructions` exactly 1, `text/plain` or
      `text/markdown`; `schema` 0..1 `application/schema+json` (optional: schema-less
      mode); `retention` 0..1 `none|default`; `max_attempts` 0..1 positive integer,
      clamped to the application's limit; unknown parts → `400`.
- [x] Order of checks: parts and limits → media type → schema compile (`422
      invalid_schema`) → representation selection (`415 unsupported_document`) →
      idempotency (next job) → quota charge (epic 05) → store blobs and row → wake
      worker → `202` with `Location` and the job body (`status: queued`, `attempts: 0`).
- [x] Nothing is written to the store or blob store before all validation passed; on a
      storage failure after blobs were written, delete them and answer `503
      unavailable`.
- [x] Tests with real multipart bodies for every row of the response table, including
      an oversized body and an oversized single part, a PNG declared as PDF, a schema
      that is not JSON Schema, a PDF with no pages and no native-capable endpoint.

## Acceptance criteria

- A 130 MiB request receives a `413` JSON body, not a reset connection (test with a
  real TCP client).
- The response body matches the contract's example for a queued job.

## Out of scope

Synchronous extraction (v0.3), JSON (non-multipart) submission (v0.3 delivery options).

## Notes

Built in `internal/api/submit.go` (handler, part rules, refusal mapping), with the job
body in `jobjson.go` (`jobResponse`, `jobToResponse`, shared with `GET /v1/jobs/{id}`).
Tests in `submit_test.go` run real multipart bodies through the full middleware chain
against SQLite and a blob directory, with a worker pool on fake providers so every
refusal is shown to call no model (and an accepted job to call two).

- `limits.request_bytes` added to the configuration (default 128 MiB, below the
  contract's minimum is a warning). The instructions (64 KiB) and schema (256 KiB)
  limits are constants, not operator settings.
- The body sits behind `http.MaxBytesReader`; every refusal first drains what is left
  up to the request limit. Over the limit, Go's server closes the connection after the
  response with a lingering close, so a client still writing sees the `413` body
  (`TestSubmitOversizedOverTCP`, 130 MiB over a raw TCP connection). `multipart.Reader`
  does not always wrap the body's error, so the body records that the limit was hit.
- Part rules beyond the contract text: a `document` part without `Content-Type` is
  `400`; instruction parts default to `text/plain` when untyped (RFC 7578); `schema`
  also accepts `application/json`; instructions must be valid UTF-8 (`400`);
  `retention` and `max_attempts` over 32 bytes are `400`; `max_attempts` must match
  `^[1-9][0-9]*$` (up to nine digits) and is stored clamped (0 = the application's).
- Error messages are fixed templates naming only service-defined part names or limits;
  document refusals map `document.Code` to a fixed message, never `document.Error.Msg`.
- What is stored is the normalized document (`doc.Data`, `doc.MediaType`: text is
  UTF-8 with `; charset=utf-8`) so the worker's `document.New` sees the same bytes.
- `attachments` (email documents) are in the `202` body only: the row has no column for
  them, so a replay or a later `GET` omits them.
- A store failure while counting, charging or storing is `503 unavailable`; blobs
  written before a failed row insert are deleted. The application's audit recorder
  (`Deps.Audit.JobCreated`) is called after the insert.

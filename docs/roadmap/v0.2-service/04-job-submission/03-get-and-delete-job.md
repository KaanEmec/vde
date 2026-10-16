# Job: Get and delete job

Version: v0.2 · Epic: 04 Job submission and retrieval · Status: done · Depends on: 01, 06-01

## Goal

`GET /v1/jobs/{id}` returns the job resource exactly as the contract shapes it, honours
ownership, and applies the first-read deletion of `retention: none`; `DELETE` removes
everything at once.

## Context

Contract §5 (resource fields and types, `404` for unknown, expired or foreign; polling
does not count against quota), §10 (`retention: none` deletes result, feedback and error
on the first terminal `GET`), §4 (`DELETE` MAY exist, `204`).

## Tasks

- [x] Response struct with exact json tags and `null` semantics (`result` object or
      null, `finished_at` null until terminal, `error` null unless failed, `feedback` an
      empty array not null); timestamps RFC 3339 UTC with second precision.
- [x] Superset fields (additive, documented in the OpenAPI): `judge_confidence` (number
      0 to 1 or null, decision 14) and `attachments` (array, present for email documents,
      v0.1 04-04; empty otherwise).
- [x] `GetJob(app, id)` scoping in SQL (`WHERE id = ? AND application_id = ?`) so a
      foreign id is indistinguishable from a missing one; expired or content-deleted
      jobs → `404`.
- [x] `retention: none` first terminal read: return the body, then in the same request
      delete `result`, `feedback`, `error`, instructions and schema from the row and mark
      `content_deleted_at` (the row stays for audit until `expires_at`, then the sweeper
      removes it). Use a transaction so a concurrent poll either sees the full body or
      `404`, never a half-deleted job.
- [x] `DELETE`: blobs, content and the row; `204`; `404` if unknown or foreign.
- [x] Polls are exempt from quota (epic 05) but subject to the per-application request
      rate limit.
- [x] Tests: body shape against the contract examples (queued, accepted, rejected),
      foreign token → `404`, first terminal read deletes, second read `404`, `DELETE`.

## Acceptance criteria

- The `Job` JSON validates against the `Job` schema in the OpenAPI document in a test.

## Out of scope

Listing jobs for callers (not in the contract; the admin API of epic 11 lists metadata for operators).

## Notes and deviations

- Handlers in `internal/api/jobs_get.go`; the body is `jobResponse`/`jobToResponse`
  (`internal/api/jobjson.go`, shared with submit). `attachments` is absent on `GET`
  (not stored on the row; the OpenAPI document says "absent otherwise").
- First terminal read of a `retention: none` job: `MarkContentDeleted(ContentAll)` runs
  before the body read from the row is written. The store clears only a job not cleared
  yet, in one `UPDATE`, so of concurrent first reads exactly one gets the body and the
  others `404` (tested with eight concurrent readers). Leftover blobs are deleted too,
  and `job.deleted` (`first_read`) is audited.
- An expired job is `404` from `expires_at` on, before the sweeper removes it. A
  content-deleted job answers `404` to `DELETE` too (the sweeper removes the row).
- `DELETE` removes the blobs, then the row, with a context that survives a client
  disconnect, and audits `job.deleted` (`request`).
- Tests: `internal/api/jobs_get_test.go` (queued/accepted/rejected/failed bodies, each
  validated against the OpenAPI `Job` schema parsed with yaml.v3; unknown, foreign,
  expired → identical `404`; first read deletes row content and blob directory; `DELETE`;
  `jobResponse` json tags equal the `Job` properties, the OpenAPI job's deferred check),
  and `cmd/vde/serve_e2e_test.go` (submit → poll → `accepted` → `404`).

Update (hardening, epic 11 agent): the email attachment list is stored on the job row
(`jobs.attachments`, JSON, migration `0004_attachments`, cleared with the rest of the
content), so `GET` and idempotent replays list `attachments` like the submit answer
(`internal/api` `TestAttachmentsPersisted`). The database runs with
`PRAGMA secure_delete=ON`. `GET /healthz` is exempt from the HTTPS rule (container health
checks and probes reach it directly, also with `trusted_proxy`; `TestHealthzWithoutHTTPS`).

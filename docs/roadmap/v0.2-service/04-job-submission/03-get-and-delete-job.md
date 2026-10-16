# Job: Get and delete job

Version: v0.2 · Epic: 04 Job submission and retrieval · Status: planned · Depends on: 01, 06-01

## Goal

`GET /v1/jobs/{id}` returns the job resource exactly as the contract shapes it, honours
ownership, and applies the first-read deletion of `retention: none`; `DELETE` removes
everything at once.

## Context

Contract §5 (resource fields and types, `404` for unknown, expired or foreign; polling
does not count against quota), §10 (`retention: none` deletes result, feedback and error
on the first terminal `GET`), §4 (`DELETE` MAY exist, `204`).

## Tasks

- [ ] Response struct with exact json tags and `null` semantics (`result` object or
      null, `finished_at` null until terminal, `error` null unless failed, `feedback` an
      empty array not null); timestamps RFC 3339 UTC with second precision.
- [ ] Superset fields (additive, documented in the OpenAPI): `judge_confidence` (number
      0 to 1 or null, decision 14) and `attachments` (array, present for email documents,
      v0.1 04-04; empty otherwise).
- [ ] `GetJob(app, id)` scoping in SQL (`WHERE id = ? AND application_id = ?`) so a
      foreign id is indistinguishable from a missing one; expired or content-deleted
      jobs → `404`.
- [ ] `retention: none` first terminal read: return the body, then in the same request
      delete `result`, `feedback`, `error`, instructions and schema from the row and mark
      `content_deleted_at` (the row stays for audit until `expires_at`, then the sweeper
      removes it). Use a transaction so a concurrent poll either sees the full body or
      `404`, never a half-deleted job.
- [ ] `DELETE`: blobs, content and the row; `204`; `404` if unknown or foreign.
- [ ] Polls are exempt from quota (epic 05) but subject to the per-application request
      rate limit.
- [ ] Tests: body shape against the contract examples (queued, accepted, rejected),
      foreign token → `404`, first terminal read deletes, second read `404`, `DELETE`.

## Acceptance criteria

- The `Job` JSON validates against the `Job` schema in the OpenAPI document in a test.

## Out of scope

Listing jobs for callers (not in the contract; the admin API of epic 11 lists metadata for operators).

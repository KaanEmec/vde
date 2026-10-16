# Job: Retention policies and deletion points

Version: v0.2 · Epic: 06 Retention and privacy · Status: in progress · Depends on: 03-04, 04-03

## Goal

One place defines what is deleted when, for `retention: none` and for the application's
default, with the operator able to force `none`.

## Context

Contract §10: `none` → delete document and pages at terminal state; delete result,
feedback and error on first terminal `GET` or 24 h after `created_at`, whichever first;
afterwards `404`; nothing survives in database, blobs, queue or cache except the audit
record. `default` → operator's configured retention; operator MUST be able to force `none`
per application. Contract §14 item 8.

## Tasks

- [x] `job.Retention` resolved at submit: request value, overridden to `none` when the
      application's `force_none` is set; stored on the row; `expires_at` derived.
- [ ] Deletion points, each a store method called from one place:
  - [x] terminal (worker): blobs for `none`; for `default`, blobs kept until `expires_at`
    (an application may also set `delete_blobs_at_terminal: true`, since results without
    documents are often enough to keep); `config.Retention.DeleteBlobsAtTerminal`;
    the sweeper does the same for jobs it finishes itself;
  - [ ] first terminal read (GET handler): content columns for `none`. Store side is
    `MarkContentDeleted(ContentAll)`; the handler call is epic 04 job 03 and is still to do;
  - [x] expiry (sweeper): everything but the audit record, for both policies.
- [ ] Caches: none exist by design (no response cache, no in-memory job cache); a test
      asserts a fresh `GET` after deletion is `404`.
- [x] Document the retention matrix in `docs/operator/retention.md`.

## Notes

`CreateJob` already resolves retention against the application (`none` requested,
`force_none`, or a `none` default all give `none`, expiry 24 h; otherwise the application's
duration), covered by `TestCreateJobRetention`. The matrix is in
[retention.md](../../../operator/retention.md). Open: the GET-handler deletion and the
fresh-`GET`-is-`404` test, both in 04-03; the acceptance test inspecting the data directory
needs that handler too. Status stays `in progress`.

## Acceptance criteria

- Contract §14 item 8 passes; after a `none` job is read, the blob directory, content
  columns and any temp files are gone (test inspects the data directory).

## Out of scope

Legal hold, per-job custom retention durations beyond `none|default`.

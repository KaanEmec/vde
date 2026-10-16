# Job: Idempotency keys

Version: v0.2 · Epic: 04 Job submission and retrieval · Status: done · Depends on: 01

## Goal

A repeated submission with the same `Idempotency-Key` from the same application within
24 hours returns the existing job and runs nothing; the same key with a different body is
refused.

## Context

Contract §4, "Idempotency": key ≤ 128 chars, per application, 24 hours, `202` (or `200`)
with the existing job and `Idempotency-Replayed: true`, `422 idempotency_mismatch` on a
different body (compare a hash of the parts). Contract §14 item 9.

## Tasks

- [x] Body hash: SHA-256 over a canonical concatenation of part name, media type and
      content hash for every part in a fixed order (sorted by name, pages by index),
      computed while streaming in the submit handler.
- [x] Store: `idempotency_key`, `body_hash`, unique index on `(application_id,
      idempotency_key)`; lookup before quota charge; match → replay the job as it is now
      (its current status), `Idempotency-Replayed: true`, no quota charge; mismatch →
      `422`.
- [x] Race: two concurrent identical submissions; the unique index makes the second
      insert fail, which is handled by re-reading and replaying.
- [x] Keys are forgotten with the job (sweeper deletes the row after `expires_at`; the
      key window is 24 hours which matches the minimum expiry; document that a key may
      be reusable after the job is gone).
- [x] Tests: replay, mismatch, concurrent race, key too long → `400`.

## Acceptance criteria

- Contract §14 item 9 passes with a provider call counter at zero on the replay.

## Out of scope

Idempotency for `DELETE`.

## Notes

Built in `internal/api/submit.go` (`submission.hash`, `replay`); the store's unique
index and `FindByIdempotencyKey` existed already. Tests in `TestSubmitIdempotency`.

- The hash covers name, declared Content-Type and SHA-256 of every part (pages as
  `page[i]` in request order); boundary, part order and filenames do not count.
- Concurrent submissions with the same key in one process are serialized by a per-key
  lock, so the second sees the first's row and replays without a quota charge. The
  unique index still guards other processes (v0.3 replicas): on `ErrDuplicate` the
  handler deletes its blobs and replays or answers `422`. In that cross-process race
  the loser's quota charge is not refunded (no decrement in the store; rare, kept
  simple).
- A key lives as long as its job row: it is reusable once the sweeper removed the job
  (`expires_at`, at least 24 hours for `retention: none`, the application's retention
  otherwise; an application retention under 24 hours shortens the key window). A key
  whose job has expired but is not swept yet replays that job; its `GET` is `404`, and
  a client retrying with the same key succeeds once the sweeper ran.
- Replays answer `202` with the job as it is now, `Location` and
  `Idempotency-Replayed: true`, after validation and before rate limits and quota.

# Job: Idempotency keys

Version: v0.2 · Epic: 04 Job submission and retrieval · Status: planned · Depends on: 01

## Goal

A repeated submission with the same `Idempotency-Key` from the same application within
24 hours returns the existing job and runs nothing; the same key with a different body is
refused.

## Context

Contract §4, "Idempotency": key ≤ 128 chars, per application, 24 hours, `202` (or `200`)
with the existing job and `Idempotency-Replayed: true`, `422 idempotency_mismatch` on a
different body (compare a hash of the parts). Contract §14 item 9.

## Tasks

- [ ] Body hash: SHA-256 over a canonical concatenation of part name, media type and
      content hash for every part in a fixed order (sorted by name, pages by index),
      computed while streaming in the submit handler.
- [ ] Store: `idempotency_key`, `body_hash`, unique index on `(application_id,
      idempotency_key)`; lookup before quota charge; match → replay the job as it is now
      (its current status), `Idempotency-Replayed: true`, no quota charge; mismatch →
      `422`.
- [ ] Race: two concurrent identical submissions; the unique index makes the second
      insert fail, which is handled by re-reading and replaying.
- [ ] Keys are forgotten with the job (sweeper deletes the row after `expires_at`; the
      key window is 24 hours which matches the minimum expiry; document that a key may
      be reusable after the job is gone).
- [ ] Tests: replay, mismatch, concurrent race, key too long → `400`.

## Acceptance criteria

- Contract §14 item 9 passes with a provider call counter at zero on the replay.

## Out of scope

Idempotency for `DELETE`.

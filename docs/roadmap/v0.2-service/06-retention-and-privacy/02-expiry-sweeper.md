# Job: Expiry sweeper

Version: v0.2 · Epic: 06 Retention and privacy · Status: planned · Depends on: 01

## Goal

A background loop in the `serve` process that removes expired jobs and their blobs, stale
quota rows, and orphan blob directories, in small batches.

## Context

Contract §10 (24 h bound, `expires_at`), §5 (`404` after expiry). ADR 0003 (sweeper is
part of the single process).

## Tasks

- [ ] Every `retention.sweep_interval` (default 1 min): `ListExpired(now, batch 100)` →
      `DeleteAll` blobs → delete row; repeat until the batch is short. Also delete
      `quota_usage` rows older than two windows and `tokens` revoked or expired more than
      90 days ago (configurable).
- [ ] Orphan blob sweep at start and daily (from the blob store job).
- [ ] Jobs still `running` past `created_at + job_timeout + grace` are finished as
      `failed/timeout` by the sweeper (a lost worker), keeping any persisted candidate.
- [ ] Metrics: swept counts; log lines without content.
- [ ] Tests with a fake clock.

## Acceptance criteria

- An expired job is `404` within one sweep interval; the data directory has no trace of
  its content.

## Out of scope

Compacting the SQLite file (`VACUUM`) automatically; document a manual command.

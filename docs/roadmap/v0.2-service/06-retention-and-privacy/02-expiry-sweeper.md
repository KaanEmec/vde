# Job: Expiry sweeper

Version: v0.2 · Epic: 06 Retention and privacy · Status: in progress · Depends on: 01

## Goal

A background loop in the `serve` process that removes expired jobs and their blobs, stale
quota rows, and orphan blob directories, in small batches.

## Context

Contract §10 (24 h bound, `expires_at`), §5 (`404` after expiry). ADR 0003 (sweeper is
part of the single process).

## Tasks

- [x] Every `retention.sweep_interval` (default 1 min): `ListExpired(now, batch 100)` →
      `DeleteAll` blobs → delete row; repeat until the batch is short. Also delete
      `quota_usage` rows older than two windows and `tokens` revoked or expired more than
      90 days ago (configurable). (`internal/sweeper`; audit rows older than
      `audit.retention` go too.)
- [x] Orphan blob sweep at start and daily (from the blob store job).
- [x] Jobs still `running` past `created_at + job_timeout + grace` are finished as
      `failed/timeout` by the sweeper (a lost worker), keeping any persisted candidate.
      The sweeper also applies the terminal-state input deletion for them.
- [x] Metrics: swept counts; log lines without content. (`sweeper.Observer.AddSwept`;
      the Prometheus counter is wired in `serve`. Log lines use the allowed keys only.)
- [x] Tests with a fake clock.

## Notes

The package is done and tested; it is not started by `serve` yet. Remaining for the wiring
step: build it in `serve` (`sweeper.New(store, blobs, Options{Interval:
cfg.Retention.SweepInterval, JobTimeout: cfg.Limits.JobTimeout, AuditRetention, Observer,
Audit, DeleteInputsAtTerminal: ids of applications with `delete_blobs_at_terminal` or
`force_none`})`, `go Run(ctx)`, stop it with the context at shutdown), the Prometheus
counter behind `Observer`, and the HTTP check that a job past `expires_at` is `404` even
before the sweep. Status stays `in progress` until then.

## Acceptance criteria

- An expired job is `404` within one sweep interval; the data directory has no trace of
  its content.

## Out of scope

Compacting the SQLite file (`VACUUM`) automatically; document a manual command.

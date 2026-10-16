# Job: Synchronous `POST /v1/extract`

Version: v0.3 · Epic: 05 Delivery options · Status: done · Depends on: v0.2 04-01

## Goal

For small documents and interactive callers: the same multipart body, the response is the
terminal job resource, held open up to a configured limit, falling back to `202` with the
job when the limit passes.

## Context

Contract §16 (MAY exist; TravelInbox does not use it).

## Tasks

- [x] Same handler path as `POST /v1/jobs` up to job creation, then wait on a
      per-job completion channel from the worker (in-process) up to `sync.max_wait`
      (default 60 s, cap 120 s); on completion return `200` with the job; otherwise
      `202` with the queued or running job and `Location`.
- [x] With multiple replicas (epic 07) the wait polls the store instead of a channel.
- [x] Counts against quota and the submit rate limit like a job; the in-progress ceiling
      applies.
- [x] OpenAPI, docs, tests.

## Outcome

`POST /v1/extract` (`internal/api/sync.go`) runs `createJob`, the code `POST /v1/jobs`
now shares with it (`submit.go`), so the body (multipart or JSON), limits, quota,
submission rate limit and in-progress ceiling are identical. It then waits on
`worker.Notifier` (woken by the pool's new `Options.OnFinish` completion hook) and reads
the store every second in any case, so a job finished by another replica is seen too.
`sync.max_wait` defaults to 60 s, capped at 120 s. Tests: `internal/api/sync_test.go`
(200 with and without the notifier, 202 fallback, limits, retention none) and
`cmd/vde/webhook_e2e_test.go` (through `vde serve`).

Deviations:

- `Notifier.Subscribe` also returns a cancel function, so a wait that ends without a
  notification does not leave its channel behind.
- A `200` counts as the job's first read: a `retention: none` job's content is deleted, as
  by a poll. A replayed `Idempotency-Key` answers `202` like `POST /v1/jobs`, without
  waiting.
- The server's 60 s `ReadTimeout` and 90 s `WriteTimeout` would end a long wait (the read
  deadline's background read cancels the request context), so the handler extends both
  deadlines to `max_wait` + 30 s once the body is read; a test checks it over TCP.

## Acceptance criteria

- A text document returns `200 accepted` in one request with the fake provider.

## Out of scope

Streaming partial results (backlog).

# Job: Synchronous `POST /v1/extract`

Version: v0.3 · Epic: 05 Delivery options · Status: planned · Depends on: v0.2 04-01

## Goal

For small documents and interactive callers: the same multipart body, the response is the
terminal job resource, held open up to a configured limit, falling back to `202` with the
job when the limit passes.

## Context

Contract §16 (MAY exist; TravelInbox does not use it).

## Tasks

- [ ] Same handler path as `POST /v1/jobs` up to job creation, then wait on a
      per-job completion channel from the worker (in-process) up to `sync.max_wait`
      (default 60 s, cap 120 s); on completion return `200` with the job; otherwise
      `202` with the queued or running job and `Location`.
- [ ] With multiple replicas (epic 07) the wait polls the store instead of a channel.
- [ ] Counts against quota and the submit rate limit like a job; the in-progress ceiling
      applies.
- [ ] OpenAPI, docs, tests.

## Acceptance criteria

- A text document returns `200 accepted` in one request with the fake provider.

## Out of scope

Streaming partial results (backlog).

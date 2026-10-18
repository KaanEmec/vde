# Job: Resource limits and backpressure

Version: v1.0 · Epic: 03 Performance · Status: done · Depends on: 01

## Goal

The service degrades predictably: a global ceiling on queued jobs, bounded memory for
uploads, and `503 unavailable` with `Retry-After` when saturated instead of slow failure.

## Context

- **Queue ceiling.** `limits.max_queued_jobs` (default `0`, no ceiling, so an upgrade changes
  nothing) counts `queued` jobs of all applications with one indexed query
  (`Store.CountQueued`, `jobs_queue` index, both drivers). The count is cached for one second
  per process and counted up by the process's own submissions, so a flood costs one query per
  second; the ceiling is soft by what other processes accept within that second. It is the
  first check of `createJob`, before any part is kept: the body is drained (not buffered) and
  the answer is `503 unavailable`, `Retry-After: 5`, no quota charged. This also refuses an
  `Idempotency-Key` replay while full (the lookup comes later); the client retries as for any
  `503`. A failed count answers `503` with the storage message (fail closed, as the
  in-progress count does). Polls do not touch the count. No dedicated metric:
  `vde_http_requests_total{status="503"}` on the two submit routes shows the refusals.
- **Streaming uploads: not done.** Every check after reading needs the bytes in memory:
  media-type sniffing, size and page checks, image conversion (the stored blob is the
  converted image), email and HTML parsing, PDF opening in pdfium, and the idempotency hash
  before anything is stored. Streaming the document part to blob storage first would need a
  second, file-backed path through `internal/document` and blobs written for refused
  requests, against principle 2. Memory in flight stays bounded by the existing limits
  (`limits.request_bytes` per multipart submission, about 2.75 times that for JSON, one
  decoded image of at most 120 megapixels), documented with the arithmetic in
  [operations](../../../operator/operations.md#memory-and-backpressure). The blob writes now
  read the bytes in place (`bytes.NewReader`) instead of copying them.
- **GOMEMLIMIT.** Guidance in the same section: about 80 % of the container limit; pdfium's
  WebAssembly memory is Go heap (wazero's default allocator) and counts against it. The
  numbers there are the limits' worst cases; measured figures come with the load test (01).

## Tasks

- [x] `limits.max_queued_jobs` (global) → `503 unavailable` at submit when exceeded.
- [x] Stream uploads to blob storage instead of buffering whole parts in memory where
      possible (document part up to 25 MiB), keeping the "read whole request before
      answering" rule. Assessed and not done (see Context); memory bounded by the limits
      and documented instead.
- [x] Soft memory limit via `GOMEMLIMIT` guidance in docs; tune based on the load test.

## Acceptance criteria

- Under a flood of submissions the service answers `503` quickly and keeps polls fast.
  (`TestSubmitQueueFull` on all submit routes, multipart and JSON, with polls answering
  `200` while full; `TestSubmitQueueCeilingCountsTheStore`.)

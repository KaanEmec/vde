# Job: Acceptance checklist as automated tests

Version: v0.2 · Epic: 09 Contract conformance · Status: done · Depends on: epics 01–08

## Goal

A black-box test suite that starts the real binary (or container) with the fake provider
and the dev configuration and walks through the ten items of contract §14 over HTTP.

## Context

Contract §14 acceptance checklist; §2 to §12 MUSTs.

## Tasks

- [x] `test/conformance/`: a Go test package that builds the binary in `TestMain`,
      starts `serve` on a free loopback port with a temp data directory and a fake
      provider script server (the fake provider as an HTTP server speaking the
      OpenAI-compatible shape, so the real adapter path is exercised), creates an
      application and tokens through the CLI, then runs one test per checklist item.
- [x] A MUST traceability table in `docs/conformance/travelinbox-v1.md`: every MUST
      sentence of the contract (numbered by section) → test name(s). Gaps are visible.
- [ ] Container mode: the same suite runs against `docker compose up` with
      `VDE_CONFORMANCE_URL` set (CI job on the release branch).
      Not done: Docker on the development machine lacks buildx, so the image could not be
      built and the compose run not tried. The suite's deployment mode
      (`VDE_CONFORMANCE_URL`, `VDE_CONFORMANCE_TOKEN`, optional
      `VDE_CONFORMANCE_FOREIGN_TOKEN`) is in place and documented in
      `docs/conformance/travelinbox-v1.md`; what is left is the release-branch CI job that
      starts compose, creates a token with `docker compose exec vde vde token create` and
      runs `go test ./test/conformance/ -count=1` against it.
- [x] A `make conformance` target, and a `conformance` job in `.github/workflows/ci.yml`.
- [x] Fix the defects the suite found (D1 to D3, below) and remove their skips:
      `TestS10NoContentSurvivesFirstRead`, `TestS04IdempotencyReplayAfterFirstRead` and
      `TestS04IdempotencyWindowOutlivesShortRetention` now pass without skipping.

## Acceptance criteria

- All ten items pass in CI against the binary; the traceability table has no row without
  a test or an explicit "not testable automatically, verified by: …".

## Out of scope

Quality evaluation (next job).

## Notes

- The quota of item 7 is spent on its own application, `travelinbox-quota` (limit 3), not on
  `travelinbox-dev`: the suite runs in the main CI test job too, with `-shuffle=on`, and
  item 3 alone submits six jobs. `travelinbox-dev` keeps retention `default`, configured as
  48 h so that it cannot be mistaken for the 24 h bound of `none`.
- Unreachable endpoints (items 5 and 6) are base URLs on a closed loopback port, so the
  adapter sees a refused connection; those tests run their own `vde serve` so that the
  marked-down endpoints do not affect `/healthz` and endpoint order in other tests.

## Defects found

Found by the suite on 2026-10-16; the service code was not changed here. Each test skipped
with its reference until the defect was fixed. All three were fixed on 2026-10-16 and the
skips removed; each entry keeps its finding and adds its resolution.

- **D1: content of a `retention: none` job survives its first terminal read in the
  database files** (contract §10: "Nothing of the job's content survives in the
  service's database … afterwards"). Test: `TestS10NoContentSurvivesFirstRead`.
  Reproduction: submit a `retention: none` job whose extractor instructions, judge
  instructions, schema (`$comment`), candidate and judge feedback each carry a unique
  marker; poll it to its terminal state (the first terminal read); the second read is 404
  as it should be; then `grep -l MARKER data_dir/*`: the instructions, schema, result and
  feedback markers are all in `vde.db-wal` (the document's bytes are not: blobs are
  deleted). The rows are cleared with UPDATE/DELETE, but SQLite keeps the old page images
  in the WAL until a checkpoint and in free pages of `vde.db` after it. Likely fix: open the
  database with `PRAGMA secure_delete = ON` and run `PRAGMA wal_checkpoint(TRUNCATE)` after
  the content deletion (first read, sweeper), or keep job content encrypted under a per-job
  key that is deleted with the content.
  **Resolved:** `secure_delete` was already on. The SQLite store now runs
  `PRAGMA wal_checkpoint(TRUNCATE)` after every statement that deletes job content
  (`MarkContentDeleted` when it cleared a row, `DeleteJob`; the sweeper and the first read
  go through them), best effort: a failure or a checkpoint kept from completing is logged
  at debug and never fails the request. Test: `internal/job`
  `TestDeletedContentLeavesNoTraceInDatabaseFiles` greps `vde.db`, `-wal` and `-shm` after
  `MarkContentDeleted(ContentAll)` and after `DeleteJob` (it fails without the
  checkpoint). Not guaranteed, documented in `docs/operator/retention.md` ("What is on
  disk") and `docs/security.md`: freed blocks the OS keeps; host disk encryption is still
  recommended.
- **D2: an idempotent replay of a `retention: none` job after its first terminal read
  answers `status: "accepted"` with `result: null`** (contract §5: "For `accepted` it is
  never `null`"). Test: `TestS04IdempotencyReplayAfterFirstRead`. Reproduction: submit a
  `retention: none` job with `Idempotency-Key: k`; poll it to `accepted` (content deleted
  on that read); submit the same request with the same key: `202`,
  `Idempotency-Replayed: true`, body `{"status":"accepted","result":null,…}`. The replay
  path (`internal/api/submit.go`, `replay`) renders the content-deleted row with
  `jobToResponse`. The client then polls and gets 404, so TravelInbox loses nothing today,
  but the body breaks §5. The Job schema requires `result`, so it cannot be left out; the
  simplest contract-safe answer to a replay of a content-deleted job is 404 `not_found`,
  the same answer a poll of it gives (to confirm with the contract owner).
  **Resolved** (decision differs from the suggestion above: 404 would break the §4 "same
  job" reading): the replay answers `202`, `Idempotency-Replayed: true`, the same id,
  `status` kept, `result` and `error` `null`, `feedback` `[]`, and a new optional job
  field `content_deleted_at` (RFC 3339) that says the content was delivered or expired
  (`internal/api/jobjson.go` `replayResponse`; absent on every pollable job). No provider
  call, no quota charge. Documented in `api/openapi.yaml` (Job, additive),
  `docs/api/guide.md` (Idempotency) and as open point 10 for TravelInbox in
  `docs/conformance/travelinbox-v1-review.md`. The test asserts this behaviour.
- **D3: the idempotency window ends with the job row, before 24 hours when
  `retention.default` is shorter** (contract §4: a repeat "within 24 hours" MUST be the same
  job and run nothing). Test: `TestS04IdempotencyWindowOutlivesShortRetention`.
  Reproduction: an application with `retention: {default: 2s}` and
  `retention.sweep_interval: 1s`; submit with `Idempotency-Key: k`, poll to terminal, wait
  4 s, submit again with the same key: `202` without `Idempotency-Replayed`, a new job id,
  and two more provider calls. `FindByIdempotencyKey` only sees rows that still exist, and
  the sweeper deletes the row at `expires_at`. TravelInbox (always `retention: none`, row
  kept 24 h) is not affected; an operator who sets a retention below 24 h is. Possible fixes:
  keep `(application, key, body hash, job id)` for 24 h independently of the job row, or
  have `vde config check` refuse (or warn about) `retention.default` below 24 h.
  **Resolved:** the job row, metadata only, is kept until max(`expires_at`, `created_at` +
  24h); GET still answers 404 from `expires_at`. The sweeper's expiry is split in two
  steps: at `expires_at` it deletes the blobs and the content of terminal jobs
  (`MarkContentDeleted(ContentAll)`, audit `job.expired`; new store method
  `ListContentExpired`), and it removes the row once both bounds are past (`ListExpired`
  now applies the 24 h floor, `job.IdempotencyWindow`). A replay in between gets the
  content-deleted form of D2. Tests: `internal/job` `TestListContentExpiredAndRowFloor`,
  `internal/sweeper` `TestShortRetentionExpiresContentAndKeepsRowForIdempotency` (fake
  clock). Documented in `docs/operator/retention.md`.


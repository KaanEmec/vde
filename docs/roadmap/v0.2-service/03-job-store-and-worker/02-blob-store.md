# Job: Filesystem blob store

Version: v0.2 · Epic: 03 Job store and worker · Status: done · Depends on: 01

## Goal

Document and page bytes stored under the data directory keyed by job id, written
atomically, deleted together, never listed or logged.

## Context

ADR 0002; contract §10 (content deleted at terminal state for `retention: none`; nothing
survives in object store, queue or cache afterwards).

## Tasks

- [x] `internal/blob.Store` interface: `Put(ctx, jobID, name, r io.Reader) error`,
      `Get(ctx, jobID, name) (io.ReadCloser, error)`, `DeleteAll(ctx, jobID) error`.
      Names are `document` and `page-01`… ; media types live on the job row.
- [x] Filesystem implementation: `<data_dir>/blobs/<first 2 chars of id>/<id>/<name>`,
      write to a temp file and rename, directory permissions 0700, files 0600.
- [x] `DeleteAll` removes the directory; idempotent; an orphan sweep at start removes
      blob directories whose job row is gone or content-deleted (crash between delete
      steps).
- [x] Tests, including the orphan sweep.

## Acceptance criteria

- After a job with `retention: none` finishes, its directory does not exist (asserted in
  the retention epic's tests through this interface).

## Out of scope

S3-compatible store (v0.3), encryption of blobs by the service (host volume encryption).

## Outcome

Done in `internal/blob` (`FS`, `NewFS(<data_dir>/blobs)`, names `blob.Document` and
`blob.Page(n)`). Deviations:

- The shard directory is the **last** two characters of the job id: the first two are
  always `j_` and the next ones a timestamp, so they would not spread anything.
- `SweepOrphans(ctx, keep func(jobID) bool) (int, error)` skips job directories modified
  within the last hour, so a submission that stores its blobs before its row is not swept
  by a periodic sweep. `keep` should return true on lookup errors.
- Job ids and blob names are validated (path safety); a failed or cancelled `Put` leaves
  no temp file.

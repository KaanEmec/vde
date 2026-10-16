# Job: Filesystem blob store

Version: v0.2 · Epic: 03 Job store and worker · Status: planned · Depends on: 01

## Goal

Document and page bytes stored under the data directory keyed by job id, written
atomically, deleted together, never listed or logged.

## Context

ADR 0002; contract §10 (content deleted at terminal state for `retention: none`; nothing
survives in object store, queue or cache afterwards).

## Tasks

- [ ] `internal/blob.Store` interface: `Put(ctx, jobID, name, r io.Reader) error`,
      `Get(ctx, jobID, name) (io.ReadCloser, error)`, `DeleteAll(ctx, jobID) error`.
      Names are `document` and `page-01`… ; media types live on the job row.
- [ ] Filesystem implementation: `<data_dir>/blobs/<first 2 chars of id>/<id>/<name>`,
      write to a temp file and rename, directory permissions 0700, files 0600.
- [ ] `DeleteAll` removes the directory; idempotent; an orphan sweep at start removes
      blob directories whose job row is gone or content-deleted (crash between delete
      steps).
- [ ] Tests, including the orphan sweep.

## Acceptance criteria

- After a job with `retention: none` finishes, its directory does not exist (asserted in
  the retention epic's tests through this interface).

## Out of scope

S3-compatible store (v0.3), encryption of blobs by the service (host volume encryption).

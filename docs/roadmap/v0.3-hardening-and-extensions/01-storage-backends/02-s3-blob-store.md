# Job: S3-compatible blob store

Version: v0.3 · Epic: 01 Storage backends · Status: planned · Depends on: v0.2 03-02

## Goal

`blobs: {driver: s3, bucket, endpoint, region, credentials from env}` stores documents and
pages in any S3-compatible store (AWS S3, MinIO, Cloudflare R2, Backblaze), so replicas
share blobs and the data directory holds nothing but the SQLite file when still used.

## Tasks

- [ ] Official AWS SDK v2 S3 client with path-style option for MinIO; keys
      `jobs/<id>/<name>`; server-side encryption header configurable (SSE-S3 default).
- [ ] `DeleteAll` lists and deletes the prefix; orphan sweep lists prefixes with no job
      row (bounded per run).
- [ ] Bucket lifecycle rule recommended in docs as a safety net for retention (expire
      after 2 days for `none`-only deployments).
- [ ] Blob store test suite from v0.2 runs against MinIO in CI.
- [ ] Document data residency implications (region) in the privacy document.

## Acceptance criteria

- Shared blob test suite passes on filesystem and S3; retention tests pass with S3.

## Out of scope

Azure Blob or GCS native drivers (both offer S3-compatible gateways; document).

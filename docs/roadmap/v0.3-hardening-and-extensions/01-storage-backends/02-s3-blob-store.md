# Job: S3-compatible blob store

Version: v0.3 · Epic: 01 Storage backends · Status: done · Depends on: v0.2 03-02

## Goal

`blobs: {driver: s3, bucket, endpoint, region, credentials from env}` stores documents and
pages in any S3-compatible store (AWS S3, MinIO, Cloudflare R2, Backblaze), so replicas
share blobs and the data directory holds nothing but the SQLite file when still used.

## Tasks

- [x] Official AWS SDK v2 S3 client with path-style option for MinIO; keys
      `jobs/<id>/<name>`; server-side encryption header configurable (SSE-S3 default).
- [x] `DeleteAll` lists and deletes the prefix; orphan sweep lists prefixes with no job
      row (bounded per run).
- [x] Bucket lifecycle rule recommended in docs as a safety net for retention (expire
      after 2 days for `none`-only deployments).
- [x] Blob store test suite from v0.2 runs against MinIO in CI.
- [x] Document data residency implications (region) in the privacy document.

## Acceptance criteria

- Shared blob test suite passes on filesystem and S3; retention tests pass with S3.

## Out of scope

Azure Blob or GCS native drivers (both offer S3-compatible gateways; document).

## Outcome

- `internal/blob/s3.go`: `blob.NewS3` (checks the bucket with `HeadBucket` at start);
  objects `jobs/<id>/<name>`; `path_style`; `sse: AES256` by default; `DeleteAll` lists
  the prefix page by page and deletes each page with `DeleteObjects`; `SweepOrphans` lists
  `jobs/` in key order, judges each job by its newest object and stops after 10 pages
  (10,000 objects), continuing after the last key at the next sweep.
- `blobs: {driver, bucket, endpoint, region, path_style, sse}`; credentials from the AWS
  default chain. Validation: bucket name, region required, endpoint URL, sse value.
- The blob suite runs as `fs` and `s3` subtests (fresh bucket per test) when
  `VDE_TEST_S3_ENDPOINT` is set, plus S3-only tests for keys and encryption, the orphan
  grace period and the bounded sweep. The `cmd/vde` end-to-end test runs a
  `retention: none` job on PostgreSQL and S3 and checks the blobs are gone.
- Docs: [storage](../../../operator/storage.md) (lifecycle rule, IAM policy, versioning,
  encryption, other stores), data residency in [privacy](../../../operator/privacy.md#data-residency).

## Deviations

- CI uses `rustfs/rustfs:1.0.1`, not MinIO: the `minio/minio` image is no longer
  pullable from Docker Hub. RustFS is S3-compatible (path-style, SSE-S3 with a master
  key); the tests use only the S3 API, so MinIO works the same.
- `Put` buffers the blob in memory (bounded by the document and page limits) because S3
  needs the length up front; no multipart upload.
- For a custom `endpoint`, the SDK sends and checks checksums only where the API requires
  them, since not every S3-compatible store accepts AWS's default trailing checksums.
- The sweeper's own test suite (`internal/sweeper`) still runs on the filesystem store;
  retention on S3 is covered by the blob suite and the end-to-end test.

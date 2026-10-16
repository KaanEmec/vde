# ADR 0002: SQLite and local files as the default storage

Status: accepted (planning), 2026-10-16

## Context

The first deployment is one container on the same VM as the consuming application. Jobs
are short-lived (minutes), retained at most 24 hours by default for the first consumer, and
the quota is in the low thousands per day. Operators want to run the service with one
provider key and nothing else.

## Decision

- Job metadata, tokens, applications, quota counters and audit records live in SQLite
  (WAL mode, single writer) via a cgo-free driver. The database file path is configured;
  a default under a data directory is used otherwise.
- Document and page bytes live in a `BlobStore`, whose first implementation is a directory
  on disk keyed by job id. Blobs are deleted with the job's content.
- `JobStore` and `BlobStore` are interfaces with exactly the methods the service uses.
  Postgres and S3-compatible implementations are v0.3 jobs and must not require changes to
  callers.
- Schema migrations are numbered SQL files embedded in the binary and applied at start.

## Consequences

- Zero-configuration local run and a single-container deployment.
- One process at a time owns the database; multiple API or worker replicas need Postgres
  (v0.3). This is acceptable for the first consumer and is documented.
- Encryption at rest is delegated to the host's disk or volume encryption; the contract
  allows this ("when the deployment's storage supports it").

# Epic 04: Job submission and retrieval

Version: v0.2 · Status: planned · Depends on: 01, 02, 03

`POST /v1/jobs` with multipart parsing under the contract's limits, early refusal codes,
idempotency, and `GET`/`DELETE /v1/jobs/{id}` with ownership and retention semantics.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Multipart parsing, limits and submit handler](01-multipart-and-submit.md) | planned |
| 02 | [Idempotency keys](02-idempotency.md) | planned |
| 03 | [Get and delete job](03-get-and-delete-job.md) | planned |

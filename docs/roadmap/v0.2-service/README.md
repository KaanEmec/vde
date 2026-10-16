# v0.2 Service

Status: in progress · Depends on: v0.1

## Goal

Wrap the v0.1 engine in the authenticated HTTP service the TravelInbox contract describes:
applications and tokens, asynchronous jobs with a persistent store and in-process worker,
quota and rate limits, retention, health and metrics, an admin API, a multi-arch
container and operator documentation. This is the first deployable release.

## Exit criterion

The acceptance checklist of contract §14 passes against a container started from the
published image with the documented minimal configuration:

1. every MUST of contract §2 to §12 holds and TravelInbox's live test passes;
2. no token, a revoked token and a foreign token get 401, 401 and 404 with no provider call;
3. text, PDF with pages, JPEG and HTML each reach a terminal state in time with
   `service_version` and `attempts` set;
4. schema violations are sent back without a judge call and end `rejected` with the
   invalid candidate kept;
5. judge group unreachable → `failed/provider_unavailable`, never `accepted`;
6. extractor primary unreachable → completes through the fallback;
7. spent quota → 429 with `X-Quota-Remaining: 0`, `X-Quota-Reset`, `Retry-After`; polls work;
8. `retention: none` deletes content at terminal and the job is 404 after being read;
9. a repeated `Idempotency-Key` returns the same job and runs no model call;
10. TravelInbox's evaluation reports a number.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [HTTP API foundation](01-http-api-foundation/README.md) | v0.1 | done |
| 02 | [Applications and tokens](02-applications-and-tokens/README.md) | 01, 03-01 | done |
| 03 | [Job store and worker](03-job-store-and-worker/README.md) | v0.1 | done |
| 04 | [Job submission and retrieval](04-job-submission/README.md) | 01, 02, 03 | done |
| 05 | [Quota and rate limits](05-quota-and-rate-limits/README.md) | 02, 04 | done |
| 06 | [Retention and privacy](06-retention-and-privacy/README.md) | 03, 04 | in progress |
| 07 | [Operations](07-operations/README.md) | 01, 03 | in progress |
| 08 | [Packaging and deployment](08-packaging-and-deployment/README.md) | 01 | done |
| 09 | [Contract conformance](09-contract-conformance/README.md) | all above | in progress |
| 10 | [Documentation](10-documentation/README.md) | all above | in progress |
| 11 | [Admin API](11-admin-api/README.md) | 02, 03 | in progress |

Epics 01, 03 and 08 can start together. Epic 11 follows 02. Epic 09 is the release gate.

## Out of scope for v0.2

Postgres, S3, Bedrock and Vertex, classifier judge, escalation, image conversion,
webhooks, synchronous endpoint, multiple replicas (all v0.3).

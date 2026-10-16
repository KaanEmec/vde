# v0.3 Hardening and extensions

Status: done (2026-10-17, see the exit criterion check below) · Depends on: v0.2

## Goal

Make the service fit deployments and consumers beyond the first one, without changing
`/v1` semantics: alternative storage, cloud-account providers and a classifier-style
judge, model escalation, image conversion, other delivery shapes, a quality evaluation
harness, and more than one replica.

Every epic here is independent and optional. Order them by demand; none blocks v1.0
except what v1.0 chooses to freeze.

## Exit criterion

A second consumer (not TravelInbox) integrates using only the API documentation, a
deployment runs with two replicas on Postgres and S3-compatible storage, and the
evaluation harness reports extraction quality per `service_version` on the TravelInbox
corpus and the service's own samples.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [Storage backends](01-storage-backends/README.md) | v0.2 | done |
| 02 | [More providers and the classifier judge](02-more-providers/README.md) | v0.2 | in progress |
| 03 | [Escalation and model tiers](03-escalation-and-model-tiers/README.md) | v0.2 | done |
| 04 | [Image conversion](04-image-conversion/README.md) | v0.2 | done |
| 05 | [Delivery options](05-delivery-options/README.md) | v0.2 | done |
| 06 | [Evaluation harness](06-evaluation-harness/README.md) | v0.2 | done |
| 07 | [Horizontal scaling](07-horizontal-scaling/README.md) | 01 | done |

Moved out of this version after the owner's decisions on 2026-10-16: Gemini (v0.1),
email documents (v0.1), admin API (v0.2). Dropped: server-side PDF rendering (ADR 0006).

## Exit criterion check (2026-10-17)

| # | Criterion | Result |
| --- | --- | --- |
| a | A second consumer integrates using only the API documentation | Not verified by a real second consumer. What exists for one: the [API guide](../../api/guide.md), JSON submission (base64 or guarded URL fetch), the synchronous `POST /v1/extract`, signed webhooks and the OpenAPI documents. |
| b | Two replicas on Postgres and S3-compatible storage | Verified. Postgres tests: 100 jobs on two worker pools each run exactly once, and a killed pool's job resumes on the other within the lease plus 1 s (`TestTwoPoolsProcessEachJobOnce`, `TestKilledWorkerResumesElsewhere`; two pools in one test binary, not two OS processes). `examples/compose/scaled` run locally with PostgreSQL 17 and RustFS: one `api` and two `worker` replicas, all healthy, jobs submitted to the API ran on the workers (07-01). |
| c | The evaluation harness reports quality per `service_version` on the TravelInbox corpus and the service's own samples | Verified. [`docs/quality.md`](../../quality.md) records runs keyed by `service_version` on 50 TravelInbox cases and 11 synthetic cases (receipts, lab reports); false accepts are the headline. |

Open after v0.3, none of which changes `/v1`:

- Bedrock and Vertex adapters have no live smoke test (no cloud credentials were available).
- The classifier judge has not been evaluated against a vendor API (unavailable); the generic
  classify contract shipped (`docs/providers/classifier.md`).
- Postgres `LISTEN/NOTIFY` was skipped; workers poll every `worker.poll_interval`.
- HEIC is not converted (no pure-Go decoder); it is accepted only where an endpoint declares it.
- `rate.shared` keeps its counters in the job store; there is no Redis, by design.
- Criterion (a) needs a real second consumer.

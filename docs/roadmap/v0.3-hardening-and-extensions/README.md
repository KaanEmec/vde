# v0.3 Hardening and extensions

Status: in progress · Depends on: v0.2

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
| 07 | [Horizontal scaling](07-horizontal-scaling/README.md) | 01 | in progress |

Moved out of this version after the owner's decisions on 2026-10-16: Gemini (v0.1),
email documents (v0.1), admin API (v0.2). Dropped: server-side PDF rendering (ADR 0006).

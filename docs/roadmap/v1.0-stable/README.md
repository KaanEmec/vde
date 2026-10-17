# v1.0 Stable

Status: planned · Depends on: v0.2 (required), v0.3 and v0.4 (whatever has shipped)

## Goal

Promise stability: `/v1` and the configuration schema are frozen with a 12-month
compatibility commitment, the service has had a security review, its performance envelope
is measured and documented, and releases are signed and reproducible.

## Exit criterion

Tag `v1.0.0` with: a published compatibility policy, a closed security review with no
open high findings, a performance baseline document, signed multi-arch images with SBOMs,
and a documentation site.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [API and configuration freeze](01-api-freeze/README.md) | v0.2, v0.3 decisions | planned |
| 02 | [Security review](02-security-review/README.md) | v0.2 | planned |
| 03 | [Performance baseline](03-performance/README.md) | v0.2 | planned |
| 04 | [Release engineering](04-release-engineering/README.md) | v0.2 08-03 | planned |

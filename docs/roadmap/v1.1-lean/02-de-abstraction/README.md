# Epic 02: Remove unneeded abstractions

Version: v1.1 · Status: in progress · Depends on: 01

Bring the code back in line with overview principle 2 ("an interface exists only where a
second implementation is planned"): interfaces and wrapper types with one production
implementation, defaults copied into packages that the validated configuration already
guarantees, and nil checks that exist only so tests can leave dependencies out. The four
jobs all touch `internal/api` or `cmd/vde/serve.go` and run one after another.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Metrics instead of observer interfaces](01-metrics-not-observers.md) | done |
| 02 | [Single-implementation interfaces and wrapper types](02-single-implementation-types.md) | planned |
| 03 | [Defaults and required dependencies from the configuration](03-defaults-from-config.md) | planned |
| 04 | [API error plumbing](04-api-error-plumbing.md) | planned |

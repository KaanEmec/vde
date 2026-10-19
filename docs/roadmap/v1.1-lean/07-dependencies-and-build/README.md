# Epic 07: Dependencies, CI and build

Version: v1.1 · Status: planned · Depends on: 00

Fewer modules where the standard library does the job in a few dozen lines, a dependency
table that matches `go.mod`, and CI and build files without redundant steps (and without
the coverage gap the audit found).

| # | Job | Status |
| --- | --- | --- |
| 01 | [Dependency hygiene and a local ULID](01-dependency-hygiene.md) | planned |
| 02 | [Prometheus text exposition without the client library](02-prometheus-exposition.md) | dropped (L4) |
| 03 | [CI, Makefile and Dockerfile](03-ci-makefile-docker.md) | planned |

# Epic 03: Endpoint groups and failover

Version: v0.1 · Status: done· Depends on: 02

An ordered list of endpoint-model pairs per group, tried in order on availability
failures, bounded in tries and total time, never crossing into the other role's group.
Content problems are not failover; they belong to the extraction loop.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Endpoint group router](01-endpoint-group-router.md) | done |
| 02 | [Failover bounds and error classification](02-failover-bounds-and-error-classification.md) | done |
| 03 | [Endpoint health marking](03-endpoint-health-marking.md) | done |

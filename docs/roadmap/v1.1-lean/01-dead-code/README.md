# Epic 01: Dead code and dead features

Version: v1.1 · Status: in progress · Depends on: 00

Remove what nothing in production reaches: test-only exports, unused fields and outputs,
and two features that exist only as stubs. Low risk; the first and largest cleanup, done
before the structural epics so they work on less code.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Test-only and unused symbols](01-test-only-symbols.md) | done |
| 02 | [Dead feature surfaces: content log sink and `--probe`](02-dead-feature-surfaces.md) | done |
| 03 | [Unused fields and outputs](03-unused-fields.md) | planned |

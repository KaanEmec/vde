# Epic 06: Tests and testdata

Version: v1.1 · Status: planned · Depends on: 00

Test code is 32,085 lines against 23,235 of production code, and `testdata/` is more than
half of all tracked files. The same scenarios run through three runners, the e2e setup is
copied six times, and the TravelInbox corpus is committed twice (once as source, once as
a generated copy). Fewer, shared tests with the same coverage.

| # | Job | Status |
| --- | --- | --- |
| 01 | [One scenario format and runner](01-one-scenario-runner.md) | planned |
| 02 | [Shared e2e harness and test fakes](02-shared-test-harness.md) | planned |
| 03 | [Eval reads the TravelInbox corpus in place](03-eval-corpus-in-place.md) | planned |

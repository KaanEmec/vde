# Epic 03: Auto decision in the loop

Version: v0.4 · Status: done · Depends on: 01, 02

The extraction loop behaves according to the job's judge policy. It sends one grounding
repair before the judge, ends in `extracted` under `never`, and accepts grounded candidates
without a judge under `auto`, with a deterministic sample still judged. See
[ADR 0009](../../../architecture/decisions/0009-judge-policy-and-grounding.md).

Exit criterion of the epic: a candidate with an invented date is never `accepted` under
`auto` without a judge, and `never` yields `extracted`. Both are scenario tests with the
fake provider.

| # | Job | Status |
| --- | --- | --- |
| 01 | [`always` and `never` paths, grounding repair](01-always-never-and-repair.md) | done |
| 02 | [`auto` gate, sampling and metrics](02-auto-gate-and-sampling.md) | done |
| 03 | [Worker, CLI and audit wiring](03-wiring.md) | done |

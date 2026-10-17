# Epic 05: Harness, docs and closeout

Version: v0.4 · Status: done · Depends on: 01, 02, 03, 04

The evaluation harness reports what the judge policy did per case. Operators and callers
get the documentation for policies, grounding and PDF preprocessing, and the version is
closed against its exit criterion. The owner's policy comparison run is not part of this
version. `docs/quality.md` gets a placeholder section for it.

| # | Job | Status |
| --- | --- | --- |
| 01 | [`vde eval`: verification, judge calls, case lists](01-eval-reporting.md) | done |
| 02 | [Operator and API documentation](02-documentation.md) | done |
| 03 | [Closeout: changelog, overview, exit criterion check](03-closeout.md) | done |

## Outcome (2026-10-18)

Done. `vde eval` reports verification, decisions and judge calls per case and in total,
takes `--judge` and comma-separated `--cases`, and `make eval` runs the corpus under each
policy. The operator and API documentation covers policies, grounding annotations, the
date table, `extracted`, `verification` and PDF preprocessing, with docs tests for the
configuration keys and the date table. v0.4 is closed against its exit criterion with the
owner-suite runs pending (see the [version README](../README.md#exit-criterion-check-2026-10-18)).

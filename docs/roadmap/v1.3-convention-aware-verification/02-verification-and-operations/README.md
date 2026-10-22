# Epic 02: Verification and operations

Version: v1.3 · Status: in progress · Depends on: job 01 on epic 01; jobs 02 and 03 none

Three changes in vde itself. Job 01 stops grounding from vouching for a verbatim copy of a
field the caller expects in a convention form, so such jobs reach the judge under `auto`.
Job 02 lets an operator check, before production, that each configured model's answers
parse in its role. Job 03 makes a failed job's error code findable after `retention: none`
has deleted the job.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Convention fields in grounding](01-convention-grounding.md) | dropped |
| 02 | [`vde config check --probe`](02-config-check-probe.md) | done |
| 03 | [Failure codes after `retention: none`](03-failure-codes-after-retention.md) | done |

Jobs 02 and 03 own disjoint files (`cmd/vde/config.go` and a probe file; `internal/audit`,
`cmd/vde/audit.go`, the admin API) and can run in parallel with each other and with epic
01. Job 01 (`internal/ground`, `internal/extract`) starts after epic 01 job 03, because it
is measured with the conventions written down.

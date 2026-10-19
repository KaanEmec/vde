# Epic 03: One source for each rule

Version: v1.1 · Status: in progress · Depends on: 01

The same rule is written in several places today: representation selection three times,
job error messages three times, token handling in the CLI and in the admin API, the local
run setup in `extract` and `eval`, the JSON-pointer helpers four times, and the
configuration checked by two validators. Each copy can drift; one has already (the
in-progress gauge after a `/v1` delete). Jobs 01 to 05 touch overlapping packages and run
in order; 06 can run in parallel.

| # | Job | Status |
| --- | --- | --- |
| 01 | [One representation selection](01-representation-selection.md) | done |
| 02 | [Job outcomes, error messages and the sweeper](02-job-outcomes-and-sweeper.md) | done |
| 03 | [CLI and admin API share one code path](03-cli-admin-shared-paths.md) | planned |
| 04 | [CLI boilerplate and one local runner](04-cli-boilerplate.md) | planned |
| 05 | [Small shared helpers](05-small-shared-helpers.md) | planned |
| 06 | [One configuration validator](06-config-single-validator.md) | planned |

# Epic 00: Preparation: skills, local CI and release, baseline

Version: v1.1 · Status: planned · Depends on: none

Runs before every other epic. First the vetted skills are installed; then CI and releases
move from GitHub Actions to the owner's machine (`make ci`, `make release`, owner
decision 18) on one branch, which is merged into `main` and pushed before any
refactoring starts, so every later job is checked by `make ci`; then the baseline is
measured with that suite.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Install and check the skills](01-skill-setup.md) | planned |
| 02 | [Local CI with `make ci`](02-local-ci.md) | done |
| 03 | [Local release to GitHub Releases and GHCR](03-local-release.md) | planned |
| 04 | [Baseline measurements and reference reports](04-baseline.md) | planned |

Milestone: 02 and 03 are merged into `main` and pushed, and GitHub Actions is off,
before 04 and before any job of epics 01 to 09 starts.

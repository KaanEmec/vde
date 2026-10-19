# Job: Measure, review and release

Version: v1.1 · Epic: 10 Closeout · Status: planned · Depends on: every other v1.1 job done or dropped

## Goal

Prove the exit criterion, review the whole diff once more, and prepare `v1.1.0`.

## Tasks

- [ ] Re-measure against the baseline.
  - Skill: EVID.
  - Prompt: "Repeat every measurement of docs/roadmap/v1.1-lean/00-preparation/02-baseline.md
    and write a before/after table into the version README's 'Exit criterion check'
    section. Report only numbers."
- [ ] Behaviour proof.
  - Skill: none (make).
  - Prompt: "Run `make check`, `make conformance`, `make scenarios`, `make eval` with
    `--compare` against eval-reports/lean-baseline under always, never and auto, and load
    every examples/*.yaml and a copy of a 1.0 configuration with each deprecated key set.
    Report pass/fail and any numeric difference."
- [ ] Whole-diff reviews.
  - Skill: REVIEW, SEC, CRITIC.
  - Prompt: `/code-review max --max-findings all v1.0.0..HEAD`, then `/security-review`,
    then "Use the architecture-critic agent (or the pasted lens) on the v1.0.0..HEAD diff:
    what abstraction or file is still there without a production reason?"
- [ ] Release notes and status.
  - Skill: none (Edit).
  - Prompt: "Write the CHANGELOG 1.1.0 entry (Removed, Deprecated, Fixed, Changed;
    numbers from the measurements), update STATUS.md, the roadmap README and this
    version's tables. Targeted edits."
- [ ] Owner tags `v1.1.0` after `make live` (owner's endpoint).
  - Skill: none.
  - Prompt: none.

## Acceptance criteria

- The exit criterion of the [version README](../README.md#exit-criterion) is met and
  recorded.

## Out of scope

- The 2.0 removals.

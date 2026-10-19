# Job: Measure, review and release

Version: v1.1 · Epic: 10 Closeout · Status: done except the release, which waits for the owner · Depends on: every other v1.1 job done or dropped

## Goal

Prove the exit criterion, review the whole diff once more, and prepare `v1.1.0`.

## Tasks

- [x] Re-measure against the baseline.
  - Skill: EVID.
  - Prompt: "Repeat every measurement of docs/roadmap/v1.1-lean/00-preparation/04-baseline.md
    and write a before/after table into the version README's 'Exit criterion check'
    section. Report only numbers."
- [x] Behaviour proof.
  - Skill: none (make).
  - Prompt: "Run `make ci`, `make scenarios`, and `make eval` with
    `--compare` against eval-reports/lean-baseline under always, never and auto, and load
    every examples/*.yaml and a copy of a 1.0 configuration with each deprecated key set.
    Report pass/fail and any numeric difference."
- [x] Whole-diff reviews.
  - Skill: REVIEW, SEC, CRITIC.
  - Prompt: `/code-review max --max-findings all v1.0.0..HEAD`, then `/security-review`,
    then "Use the architecture-critic agent (or the pasted lens) on the v1.0.0..HEAD diff:
    what abstraction or file is still there without a production reason?"
- [x] Release notes and status.
  - Skill: none (Edit).
  - Prompt: "Write the CHANGELOG 1.1.0 entry (Removed, Deprecated, Fixed, Changed;
    numbers from the measurements), update STATUS.md, the roadmap README and this
    version's tables. Targeted edits."
- [ ] Owner releases `v1.1.0` locally after `make live` (owner's endpoint): `make
      release VERSION=v1.1.0 DRY_RUN=1`, then without `DRY_RUN` (00-03).
  - Skill: none.
  - Prompt: none.

## Acceptance criteria

- The exit criterion of the [version README](../README.md#exit-criterion) is met and
  recorded.

## Out of scope

- The 2.0 removals.

## Result

- Measurements and the verdict per criterion: the version README,
  [Exit criterion check (2026-10-20)](../README.md#exit-criterion-check-2026-10-20).
- Behaviour proof: `make ci` green on the final tree (both toolchains, conformance, eval,
  storage); the three `make eval` reports equal the `eval-reports/lean-baseline/` ones
  under `always`, `never` and `auto` except timings; `make scenarios` green; every 1.0
  configuration file (`examples/*.yaml`, `examples/compose/**/vde.yaml`, the scenario
  config at `v1.0.0`) passes `vde config check`.
- Reviews: each epic got its whole-diff review when it closed (the `fix(lean): review of
  epic NN` commits), then a final security and leftover pass over `v1.0.0..HEAD`
  (`chore(lean): final review leftovers`: `document.New` takes the validated limits
  without its own copy of the defaults, comments no longer cite roadmap jobs or versions).
- Release notes: CHANGELOG `[Unreleased]` is the 1.1.0 entry; STATUS.md updated.
- Release: not done; the owner asks for it (task above).

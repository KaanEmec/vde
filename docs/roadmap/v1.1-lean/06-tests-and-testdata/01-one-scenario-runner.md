# Job: One scenario format and runner

Version: v1.1 · Epic: 06 Tests and testdata · Status: planned · Depends on: 05-01

## Goal

Every loop scenario is a YAML file in `testdata/scenarios/` in one format, run by one
runner; Go table tests that duplicate a scenario case for case are gone. Tests only.

## Context

- `testdata/scenarios/*.yaml` (14 files) run in `cmd/vde` `TestScenarios`
  (`extract_test.go:120`, through `extract.Run` and the CLI) and again three times per
  file in `internal/extract` `TestRun_scenarioSuiteUnchanged`.
- `testdata/scenarios/judge-policy/*.yaml` (19 files) use a second format with their own
  runner (`TestRun_judgePolicyScenarios` 94 lines, `policyScenario`,
  `scenarioRunInput` 91 lines).
- `TestRun_judgePolicy` (`extract_test.go:981`, 102 lines, 13 cases) overlaps the
  judge-policy files case for case; about ten hand-written `TestRun_*` tests repeat
  top-level scenarios (acceptFirstTry, unparseableThenAccept, rejectRepaired,
  attemptsExhausted, extractorFailover, judgeGroupDown, deadline, judgeUnparseable,
  confidence, schemaless).
- `make scenarios` reruns `TestScenarios` verbosely (00-02 decides the target).
- About −250 to −450 test lines.

## Tasks

- [ ] Map overlap.
  - Skill: PRTK (`pr-test-analyzer`).
  - Prompt: "Use the pr-test-analyzer agent read-only on internal/extract/*_test.go,
    cmd/vde/extract_test.go and testdata/scenarios: for each Go test case, name the
    scenario file that covers the same inputs and assertions, or 'unique'. Report a
    table only."
- [ ] Unify the format and runner.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: convert the judge-policy scenarios to the
    top-level scenario format (extend the format only where needed), run all scenario
    files through the cmd/vde runner, delete the second runner and the extract-level
    rerun, then delete Go test cases the table marked as covered. No assertion may be
    lost: every deleted case maps to a scenario. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Gate.
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review medium <first>^..<last>`, then
    `/pr-review-toolkit:review-pr tests`.

## Acceptance criteria

- One scenario runner; `go test -cover ./internal/extract` not lower than the baseline;
  `make scenarios` green.

## Out of scope

- Changing what a scenario asserts.

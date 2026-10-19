# Job: One scenario format and runner

Version: v1.1 · Epic: 06 Tests and testdata · Status: done · Depends on: 05-01

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

- [x] Map overlap (by hand, no agent; table in Result).
  - Skill: PRTK (`pr-test-analyzer`).
  - Prompt: "Use the pr-test-analyzer agent read-only on internal/extract/*_test.go,
    cmd/vde/extract_test.go and testdata/scenarios: for each Go test case, name the
    scenario file that covers the same inputs and assertions, or 'unique'. Report a
    table only."
- [x] Unify the format and runner.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: convert the judge-policy scenarios to the
    top-level scenario format (extend the format only where needed), run all scenario
    files through the cmd/vde runner, delete the second runner and the extract-level
    rerun, then delete Go test cases the table marked as covered. No assertion may be
    lost: every deleted case maps to a scenario. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review medium <first>^..<last>`, then
    `/pr-review-toolkit:review-pr tests`.

## Acceptance criteria

- One scenario runner; `go test -cover ./internal/extract` not lower than the baseline;
  `make scenarios` green.

## Result

The 19 `judge-policy/` files use the top-level format: `input.judge` and
`input.sample_rate` (default `config.DefaultSampleRate`, as in the CLI) are new input
keys; `verification`, `feedback_sources`, `decision` and `disagreements` new expect keys,
each compared when present (`decision`, `disagreements` and the outcome's judge-call
count only in process: the command does not print them); `extractor_calls: N` became
`{primary: N}`. `TestScenarios` (`cmd/vde`) runs all 33 files through `extract.Run` and,
where the command can reproduce the input, through `vde extract --judge`: four run in
process only (logged), because `vde extract` has no sample-rate flag
(`auto-sampled-agreement`, `auto-sampled-disagreement`, and `auto-ungroundable-image`,
whose document the default rate samples, though it never reaches the gate) and always
reads a PDF's text with its engine (`auto-ungroundable-pdf`). The in-process path now
uses `newLocalRun` and `localRun.input`, so it takes the judge policy, sample key and
`service_version` exactly as the command does. The rerun of `TestRun_scenarioSuiteUnchanged`
moved into `TestScenarios`: every scenario under the configured policy also runs with
the loop options at their off values and without grounding text, and must give the same
outcome, calls and `service_version`. The "never accepted on grounding with the invented
organiser" check applies to every scenario. Deleted: `TestRun_judgePolicyScenarios`,
`policyScenario`, `TestRun_scenarioSuiteUnchanged`, `scenarioRunInput`, `runScenario`,
`scenarioInput`, `scenarioTypes`, `offKeys` (`internal/extract/extract_tiers_test.go`).
Mutation check: a wrong `disagreements` or `feedback_sources` in a scenario fails it.

Overlap map (Go test case → scenario). "Unique" cases assert what no scenario does
(prompt text, framings, models, re-ask reminder, progress callbacks, elapsed time,
grounding entry text) and stay; deleted ones are marked.

| Go test case (`internal/extract`) | Scenario | Action |
| --- | --- | --- |
| `TestRun_extractorFailover` | `extractor-primary-down` (one primary call, one fallback call by model, accepted 1/1) | deleted |
| `TestRun_judgeGroupDown` | `judge-group-down` (failed, `provider_unavailable`, 1 attempt, calls 1/1, the unjudged result) | deleted |
| `TestRun_judgePolicy` "always repair fails, judge rejects to the limit" | `always-repair-bounded` | deleted |
| `TestRun_judgePolicy` "always unannotated, no repair" | `always-unannotated-no-repair` | deleted |
| `TestRun_judgePolicy` "never" | `never-grounded` | deleted |
| `TestRun_judgePolicy` "never schema violations" | `never-schema-violation` (decision "") | deleted |
| `TestRun_judgePolicy` "auto sampled" | `auto-sampled-agreement` | deleted; replaced by "auto sampled, the judge disagrees" (mirrors `auto-sampled-disagreement`), the only in-package cover of the disagreement count |
| `TestRun_judgePolicy` "auto ungrounded after repair, advisory entries" | `auto-invented-repair-fails` | deleted |
| `TestRun_judgePolicy` "auto grounded" | `auto-grounded` | kept: the only in-package cover of the grounded acceptance (package coverage) |
| `TestRun_judgePolicy` "always grounded", "always repaired", "always schema-less", "never advisory", "never schema-less", "never failed extractor", "auto judge rejects then grounded answer" | none, or the scenario lacks the prompt, progress or entry-text checks | unique, kept |
| `TestRun_acceptFirstTry`, `rejectRepaired`, `unparseableThenAccept`, `schemaless` | `accept-first-try`, `judge-reject-then-accept`, `unparseable-then-accept`, `schemaless-accept` | unique (models, framings, retry and judge prompt contents), kept |
| `TestRun_attemptsExhausted` | `three-rejections` | unique (two feedback entries incl. pointer "", confidence on a rejection, a schema violation in between), kept |
| `TestRun_deadline` | `slow-extractor-timeout` | unique (returns within 5 s of the deadline), kept |
| `TestRun_judgeUnparseable` (5), `TestRun_confidence` (4) | `judge-garbage-twice`, `judge-confidence` | unique (re-ask reminder; absent, out-of-range and zero confidence), kept |
| every other `TestRun_*`, `extract_tiers_test.go` | none | unique, kept |

Coverage (`go test -cover`): `internal/extract` 92.3 % → 92.3 %, `cmd/vde` 81.3 % →
81.3 %. Test lines −183 (+152 −335), short of the −250 estimate: most hand-written
`TestRun_*` assert prompt contents no scenario checks. `make check`, `make scenarios`
green.

`git diff --stat`: production 0 files; tests 3 files (+152 −335); testdata 19 files
(+57 −95: header comment and `extractor_calls` form); docs 1 (the v0.4 exit criterion
names the new runner).

## Out of scope

- Changing what a scenario asserts.

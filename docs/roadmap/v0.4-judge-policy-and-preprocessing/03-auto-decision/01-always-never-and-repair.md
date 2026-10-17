# Job: `always` and `never` paths, grounding repair

Version: v0.4 · Epic: 03 Auto decision in the loop · Status: in progress · Depends on: 01-02, 02-03

## Goal

`extract.Run` takes the job's policy. Under `always` and `auto`, a schema-valid candidate
with ungrounded critical leaves goes back to the extractor once before any judge call. Under
`never`, the loop ends `extracted` without a judge.

## Context

ADR 0009. The repair costs one extractor call and no judge call. It happens at most once per
job and only while an attempt remains, so `max_attempts` still bounds the extractor calls,
and the last attempt is never spent on it. Under `always`, grounding entries never reach the
job's `feedback` (contract v1 sources only). Under `never` they are advisory on `extracted`.

## Tasks

- [ ] `internal/extract/extract.go`: `Input.JudgePolicy config.JudgePolicy`,
      `Input.SampleRate float64`, `Input.SampleKey []byte` (used by job 02).
      `Outcome.Verification` and `Outcome.JudgeCalls int` (judge and second-judge calls
      that answered, for the eval report and metrics).
- [ ] After schema validation, when the representation is groundable: run `ground.Check`
      with `Schema.GroundingRules()`. If critical leaves are missing, no repair has been
      made in this run (`PriorFeedback` without `grounding` entries on resume), and
      `attempt < limit`, then set the attempt's feedback to the grounding entries and
      continue without a judge call. `OnAttempt` persists them as for schema feedback.
- [ ] `always`: otherwise judge as in v0.3. Accept gives `verification: judge`. Grounding
      entries are dropped from the outcome's feedback.
- [ ] `never`: a schema-valid candidate (any parseable JSON object when schema-less) ends
      `StatusExtracted`, `verification: none`, and its feedback is the grounding entries
      when the representation is groundable, else empty. No judge or second judge is asked.
      Schema violations still retry up to the limit and then end `rejected`.
- [ ] Framing: grounding feedback reaches the extractor through the existing retry framing
      (`internal/extract/framings`). Check that the wording "value not found in the
      document" reads correctly there, and add no new framing unless a test shows a need.
- [ ] `internal/extract/version.go`: hash `ground.RulesVersion` under `always` as well,
      since the repair changes the loop. Every `service_version` changes once in v0.4, and
      the CHANGELOG says so. Update `version_test.go`.
- [ ] `docs/operator/framings.md`: mention `grounding` feedback in the retry framing.

## Acceptance criteria

Scenario tests in `internal/extract` with the fake provider (new scripts under
`testdata/scenarios/judge-policy/`):

- `always`, invented date on attempt 1, corrected on attempt 2: 2 extractor calls, 1 judge
  call, `accepted`, `verification: judge`, no `grounding` source in the feedback.
- `always`, invented date persists after the repair: the judge is asked on attempt 2. If it
  accepts, the job is `accepted` with `verification: judge` (judge authority, contract §5).
- `always` with `max_attempts: 1`: no repair, the judge is asked at once.
- `never`, valid candidate: 1 extractor call, 0 judge calls, `extracted`,
  `verification: none`, grounding entries present as advisory.
- `never`, schema violation on every attempt: `rejected`.
- `TestRun_scenarioSuiteUnchanged` still passes for every v0.3 script whose candidates are
  grounded (scripts with ungrounded candidates are listed in the test with their new call
  counts).

## Out of scope

`auto` (job 02); grounding of PDFs (epic 04).

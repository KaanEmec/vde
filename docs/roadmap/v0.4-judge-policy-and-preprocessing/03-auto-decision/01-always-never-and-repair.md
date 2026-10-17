# Job: `always` and `never` paths, grounding repair

Version: v0.4 · Epic: 03 Auto decision in the loop · Status: done · Depends on: 01-02, 02-03

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

- [x] `internal/extract/extract.go`: `Input.JudgePolicy config.JudgePolicy`,
      `Input.SampleRate float64`, `Input.SampleKey []byte` (used by job 02).
      `Outcome.Verification` and `Outcome.JudgeCalls int` (judge and second-judge calls
      that answered, for the eval report and metrics).
- [x] After schema validation, when the representation is groundable: run `ground.Check`
      with `Schema.GroundingRules()`. If critical leaves are missing, no repair has been
      made in this run (`PriorFeedback` without `grounding` entries on resume), and
      `attempt < limit`, then set the attempt's feedback to the grounding entries and
      continue without a judge call. `OnAttempt` persists them as for schema feedback.
- [x] `always`: otherwise judge as in v0.3. Accept gives `verification: judge`. Grounding
      entries are dropped from the outcome's feedback.
- [x] `never`: a schema-valid candidate (any parseable JSON object when schema-less) ends
      `StatusExtracted`, `verification: none`, and its feedback is the grounding entries
      when the representation is groundable, else empty. No judge or second judge is asked.
      Schema violations still retry up to the limit and then end `rejected`.
- [x] Framing: grounding feedback reaches the extractor through the existing retry framing
      (`internal/extract/framings`). Check that the wording "value not found in the
      document" reads correctly there, and add no new framing unless a test shows a need.
- [x] `internal/extract/version.go`: hash `ground.RulesVersion` under `always` as well,
      since the repair changes the loop. Every `service_version` changes once in v0.4, and
      the CHANGELOG says so. Update `version_test.go`.
- [x] `docs/operator/framings.md`: mention `grounding` feedback in the retry framing.

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

## Result (2026-10-17)

Done. The loop (`internal/extract/extract.go`, `loop.run` with the helper `loop.ground`)
grounds every schema-valid candidate when the representation is groundable, builds the
`ground.Text` index once per run, and decides by policy: `never` ends `extracted` with the
grounding entries as advisory feedback; under `always` and `auto` a candidate with critical
misses goes back to the extractor once (`SourceGrounding` entries through the existing
retry framing, persisted by `OnAttempt`) while an attempt remains, then the judge decides.
`CompileSchema` reads the annotations (`ground.RulesFromSchema`; an unknown value is
`invalid_schema`) and `(*Schema).GroundingRules()` returns them.

Tests: 17 scripts in `testdata/scenarios/judge-policy/` run by
`TestRun_judgePolicyScenarios`, plus `TestRun_judgePolicy` (12 cases),
`TestRun_autoNeverGroundsInvented` (48 variants), `TestRun_groundingRepairOnResume`,
`TestRun_groundingRepairDroppedOnFailure`, `TestCompileSchema_grounding`,
`TestServiceVersion_v04Fields` and `TestServiceVersion_pdfPolicy`.

Deviations:

- `Outcome` also gains `Decision` and `Disagreements` (metadata for the metrics, job 02);
  all three new fields are `json:"-"`.
- "No repair made in this run" is read from `PriorFeedback`: any `grounding` entry means
  the repair was made (conservative on resume).
- Under `always` the grounding entries are removed in one place, `loop.outcome`, so they
  never reach the job's feedback even when the attempt after a repair fails or times out;
  the worker drops them from a persisted row too when it ends a job without the loop
  (`fromRow`, a deadline passed after a repair).
- `ground.Check` runs with `MaxMisses: 50`, the schema feedback cap.
- No v0.3 script needed new call counts: six scripts had fake candidates their document
  does not print (`Ada Lovelace` on `booking.txt`, a total of `421.50`). They now carry
  printed values (`Jane Example`, 3 nights; a wrong but printed total, `360.00`), so their
  stories are unchanged, and `TestRun_scenarioSuiteUnchanged` also runs every script with
  the representation made ungroundable (the v0.3 loop) and asserts identical outcomes and
  calls. Tests in `cmd/vde`, `internal/worker`, `internal/logging` and the fake's own test
  follow the new fixture values.
- `service_version`: `ground.RulesVersion` is hashed under every policy; the PDF policy
  string (`document.PDFPolicy.String()`) when a role's groups do not all read PDFs
  natively and the mode is not `native`, or when the policy is not `always`.
  `TestServiceVersion_v04Fields` pins the v0.3 hashes of two configurations and shows
  that removing the two fields restores them.
- Framing: no new text. `- "/date" (grounding): value not found in the document` reads
  correctly; `docs/operator/framings.md` says so.

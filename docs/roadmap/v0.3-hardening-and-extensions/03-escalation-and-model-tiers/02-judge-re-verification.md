# Job: Judge-only re-verification option

Version: v0.3 · Epic: 03 Escalation and model tiers · Status: done · Depends on: 01

## Goal

Optional per-application setting to run a second, independent judge (another group) on
an accepted result before reporting `accepted`, for callers who want two opinions on
high-stakes documents.

## Tasks

- [x] `roles.second_judge: {group, instructions: inherit}` optional; when set, an
      `accept` from the first judge is confirmed by the second; a `reject` from the
      second feeds the next attempt; both failures follow the same never-accept rules.
- [x] Only when configured; hashed into `service_version`; metrics.
- [x] Decide after consumer demand; keep as a small addition to the loop, not a
      generalized "judge pipeline".

## Acceptance criteria

- Scenario tests; off by default with no behaviour change.

## Out of scope

Majority voting across many judges.

## Outcome

- `roles.second_judge: {group}`; validated like a judge group. `extract.Roles.SecondJudge`
  is asked, with the same judge framing and request, only after the judge accepts. Its
  accept accepts (advisory entries of both judges kept, `judge_confidence` the second
  judge's); its reject is the attempt's feedback; an unparseable verdict is
  `judge_failed`, an unavailable group `provider_unavailable`.
- Hashed into `service_version` only when set. Off by default:
  `TestRun_scenarioSuiteUnchanged` runs every `testdata/scenarios` script with the options
  absent and spelled out at their off values and asserts identical outcomes, calls and
  `service_version`.
- Tests: `TestRun_secondJudge` (both accept, second rejects then repaired, first rejects
  so the second is not asked, second rejects the last attempt, unparseable, group down).

Deviations:

- Process-wide, not per application: `roles` are global configuration, and a
  per-application role set would need a second loop wiring. Instructions are always
  inherited, so the `instructions` key does not exist.
- Metrics: second judge calls carry the judge framing, so they are counted under the
  `judge` role label; a separate label would need a different framing and was skipped.

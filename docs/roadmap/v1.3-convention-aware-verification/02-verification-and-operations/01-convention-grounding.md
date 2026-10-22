# Job: Convention fields in grounding

Version: v1.3 · Epic: 02 Verification and operations · Status: dropped · Depends on: epic 01 job 03

Dropped 2026-10-22 after measurement. With `provider` annotated `convention`, every job went
to the judge (5 of 5 instead of 0), input tokens rose 66% and output tokens 125%, and the
judge (`z-ai/glm-5.3-flash`) accepted all five on the first attempt, three with wrong values:
no outcome changed, 7 mismatches before and after (see
[quality](../../../quality.md#v13-convention-aware-verification-2026-10-22)). The value counted
as a copy whenever it was part of the printed text, so correct values went to the judge too.
The implementation is commit `6157cec`, reverted in the next commit; worth revisiting only
with a judge that rejects such values (check it with `vde config check --probe` and a
measurement).

## Goal

A new `x-vde-grounding` value, `convention`, for fields the caller expects in a form other
than the printed one (a brand name, a number without its label). For such a field a
verbatim copy of the document is a warning, not evidence: under `auto` it sends the job to
the judge instead of letting grounding accept it.

## Context

Grounding (`internal/ground`, ADR 0009) accepts a string leaf when its normalized form
occurs in the document text on word boundaries. Under `auto` the gate in
`internal/extract/extract.go` (`decide`) skips the judge when every critical leaf is
grounded and the candidate answers no schema or judge feedback. TravelInbox's remaining
misses (`WIZZ`, `DB Fernverkehr AG`, `COURTYARD by Marriott`, `Wg. 7`) are all printed, so
they pass the gate and no judge ever sees them.

Today's values: `critical`, `check`, `derived`, `skip`. Proposed semantics of
`convention`, per leaf:

- found verbatim (today's string match) → the leaf is a convention copy: not a miss (no
  grounding repair, no feedback to the extractor), but the `auto` gate fails and the job
  goes to the judge;
- not found → treated as `derived` (a normalized form is expected not to be printed);
- `always` and `never` are unaffected: under `always` the judge sees every job anyway,
  under `never` there is no judge.

The cost is judge calls on jobs whose convention value happens to equal the printed form
(for example a brand printed exactly as expected). Epic 01's conventions in the judge
instructions are what lets the judge reject the wrong copies. If the measured judge calls
are too many for the fields fixed, a refinement is to count only matches whose printed
case differs from a title-cased form, decided here by measurement, not up front.

## Tasks

- [x] ADR 0009 amendment: the `convention` value, its semantics under each policy, and
      why a verbatim copy is not evidence for such fields.
- [x] `internal/ground`: accept `convention` in `RulesFromSchema` (inherited like the
      others, overridable by a deeper annotation); report convention copies as a count and
      their pointers in the result, never values; bump `RulesVersion` to `ground-3`.
- [x] `internal/extract`: the `auto` gate also requires zero convention copies; no
      grounding repair and no grounding feedback for them. Keep `decide` readable (named
      step, as v1.1 split it).
- [x] Scenarios under `testdata/scenarios/judge-policy/`: a convention copy under `auto`
      goes to the judge; a normalized convention value is grounded through; `always` and
      `never` unchanged; a convention leaf inside a `critical` object.
- [x] Docs: the value table in [judge-policy.md](../../../operator/judge-policy.md),
      [writing instructions](../../../api/writing-instructions.md) ("Annotating the
      schema for grounding"), CHANGELOG.
- [x] Annotate `provider` and the convention `details` fields in
      `testdata/travelinbox/corpus/schema.json` (epic 01's copy; annotated: `provider`,
      `flight` and `flight_leg` `seat`, `cabin`, `terminal`, `rail` `coach` and `seat`) and run the comparison
      against epic 01 job 03's report: fields right, judge calls, tokens and cost per
      case. Keep or revert; add the annotations to the hand-over document of epic 01
      job 03 when kept.

## Acceptance criteria

- `internal/ground` tests (table-driven): a verbatim convention leaf is a copy, not a
  miss; a non-printed convention leaf is neither; inheritance and override.
- `TestRun_autoNeverGroundsInvented` and the existing judge-policy scenarios pass
  unchanged; the new scenarios pass.
- An unknown `x-vde-grounding` value is still `422 invalid_schema`; a schema without
  `convention` gives byte-identical results and the same `service_version` apart from
  `ground-3`.
- The comparison is recorded with the change in judge calls next to the fields fixed.

## Out of scope

Learning conventions from history (shape mining), lookup tables for brand names.

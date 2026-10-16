# ADR 0008: Judge quality is measured, not assumed

Status: accepted, 2026-10-17

## Context

The judge exists to keep wrong answers from being accepted, but nothing measured whether it
does. v0.3 added `vde eval` (see [evaluation](../../operator/evaluation.md)): a corpus of
documents with ground truth, a per-case comparison rule file, and a report keyed by
`service_version`. The first real run is in [docs/quality.md](../../quality.md): 50
TravelInbox cases, one model (`google/gemini-3.8-flash`) in both roles, every case accepted
on the first attempt, and 43 of the 50 accepted answers differing from the ground truth in
at least one compared field.

Reading the mismatching pointers (values were not inspected) shows that most are not
hallucinations. They sit where the corpus conventions are stricter than the instructions
the roles were given: `trip_hint/ends_on` null for one-way documents, traveller `type` only
when stated, place `name` null when only a city and code are printed, the operator rather
than the booking site as `provider`. The judge checks the candidate against the document,
not against conventions it was never told, so it accepts. Judge leniency is real as well:
a same-model judge shares the extractor's reading of the document. The v0.2 live test
(`02-travelinbox-live-test.md`) had already shown the same pattern as a low no-correction
share.

## Decision

- **The false-accept rate (accepted answers that differ from the ground truth) is the
  headline quality metric**, reported with acceptance, field accuracy and attempts per
  `service_version`. Real runs are manual and recorded in `docs/quality.md`; CI checks only
  the harness mechanics against the fake provider.
- **Model choice is measured, not assumed.** A model, a judge arrangement (same model,
  different model, second judge, classifier) or a prompt change is adopted on a measured
  run against the corpus, never on its price or reputation.
- **The comparison rules are not loosened to hide disagreement.** A mismatch is first
  classified as a model error, a judge miss or a ground-truth convention disagreement;
  only the last is fixed by changing instructions or the corpus, and the fix is a
  conscious decision.
- **TravelInbox instructions and compare rules need alignment**, starting with
  `trip_hint/ends_on` and `travellers/N/type`: the extractor and judge instructions must
  state the convention explicitly, or the compare rule must reflect what the contract
  actually requires. The service holds no domain knowledge, so this lives in the corpus
  and in TravelInbox's instructions, not in the engine.

## Consequences

- Quality claims in the documentation cite a run in `docs/quality.md` or are not made.
- A new run after instruction alignment, and one with a different judge model or the
  classifier judge once a vendor API is available, are the next measurements.
- The harness's own corpus (synthetic receipts and lab reports, 11 cases) keeps the service's
  quality visible without TravelInbox data.

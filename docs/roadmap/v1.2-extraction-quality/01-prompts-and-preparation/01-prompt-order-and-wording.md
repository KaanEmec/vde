# Job: Prompt order and wording

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: done · Depends on: v1.1 (released)

Done 2026-10-20: compared and kept, see [quality](../../../quality.md#v12-prompts-and-document-preparation-2026-10-20).

## Goal

Give both roles the document first and the task last, and remove the two contradictions in
the extractor framing: null versus omitting a field, and "take every value from the
document" versus normalising values as the caller instructs.

## Context

`internal/extract/framing.go` builds the parts in this order: caller instructions, schema,
document, then (extractor retry) previous candidate and feedback, or (judge) candidate.
Anthropic reports up to 30% better answers on long inputs with the document above the
instructions and the question last. The framings
(`internal/extract/framings/extractor.md`, `judge.md`) describe the order, so they change
with it.

The extractor framing says "use null or leave the field out, as the schema and the
instructions allow" and "Take every value from the document … Never guess". A caller that
asks for ISO dates, split amounts or canonical codes asks for values that are not verbatim
in the document, so the model has to choose between two rules, and runs differ.

## Tasks

- [x] Record the baseline report at the `v1.1.0` commit: one run on the four core cases
      (epic README, "Measuring"), nothing more.
- [x] `framing.go`: order the extractor parts as document, schema, caller instructions,
      previous candidate, feedback; the judge parts as document, schema, caller
      instructions, candidate. Keep the separators.
- [x] `framings/extractor.md` and `judge.md`: describe the new order.
- [x] `extractor.md`: one rule for missing values: when the schema allows null, use null
      for a value the document does not give; leave a field out only when the schema does
      not list it as required and does not allow null.
- [x] `extractor.md`: say that formatting, normalising and converting a value the document
      gives, as the instructions ask (date formats, units, codes, splitting a value), is
      expected; inventing a value the document does not give is not.
- [x] `judge.md`: the same distinction, so a correctly normalised value is not rejected as
      "not what the document says".
- [x] Contract deviation (owner decision 2026-10-20: the new order and jobs 05 and 06's
      answer blocks become the default; TravelInbox is asked to amend the contract):
      ADR 0012 (0011 was taken) records the deviation from contract §6 steps 2 and 5 (order, "no prose")
      and §7 (the verdict as the judge's whole answer), amending ADR 0005 for these
      points; `docs/conformance/travelinbox-v1.md` marks rows 6.3, 6.9 (step 5; 6.6 is step 3) and 7.x as
      "deviation, amendment proposed"; `docs/conformance/contract-amendment-v1.2.md` is the
      proposal the owner sends to TravelInbox (the agent does not send it); conformance
      tests that pin the old order are updated to the new one.
- [x] Update `docs/api/writing-instructions.md`, "How the prompts are assembled".
- [x] Run the comparison, record it in `docs/quality/`, keep or revert.

## Acceptance criteria

- `framing_test.go`: the part order for the extractor (first attempt and retry) and the
  judge; the document part is first in both.
- `version_test.go`: `service_version` changes.
- `make scenarios`, `make conformance` and `make eval` pass.
- The comparison against the baseline is recorded with the keep or revert decision.

## Out of scope

The answer format (job 05), the judge's answer (06), schema descriptions (08).

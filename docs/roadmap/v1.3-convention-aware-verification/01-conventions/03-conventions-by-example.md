# Job: Conventions by example, and the hand-over

Version: v1.3 · Epic: 01 Conventions in schema and instructions · Status: planned · Depends on: 02

## Goal

State the conventions a schema cannot check, above all brand names, as short examples in
the extractor and judge instructions, measure them, and hand the measured schema and
instruction changes to TravelInbox with answers to the questions of their report.

## Context

Models follow examples more reliably than abstract rules. The conventions without a
checkable form: the provider as the brand customers know (not the operating company, a
sub-brand or the capitalised logo text), and which of two printed amounts is the price
of a cancellation. The judge needs the same examples, or it has no rule to reject a
verbatim copy (epic 02 job 01 sends such copies to it).

Examples are written on synthetic values that appear in no corpus document (an invented
airline, rail operator and hotel chain), so the measurement on the five cases is not
leaked. The instructions are caller text: vde inserts them verbatim, so the examples are
TravelInbox's proposal, not part of vde's framing.

## Tasks

- [ ] Add a "Conventions" section with three to five "as printed → as expected" examples
      per convention to `testdata/travelinbox/corpus/extractor.md`: provider brand form,
      coach, seat and terminal without labels (mirroring job 02's constraints), cabin
      class, traveller type from party counts, the price of a cancellation.
- [ ] Add the same examples to `judge.md`, phrased as what to reject.
- [ ] Bump `prompt_version` in both files and note it in `testdata/travelinbox/README.md`.
- [ ] Run the comparison against job 02's report; record it with `provider` and
      `cancellation` mismatches and judge calls; keep or revert per convention.
- [ ] Write `docs/conformance/travelinbox-conventions-v1.3.md` for the owner to send: the
      schema diff and instruction diff as proposals for TravelInbox's job
      `v1.0-launch/04-vde/06-extraction-quality`, the measured effect per change, and
      short answers to the report's questions 2 (failure codes: epic 02 job 03),
      3 (judge models: epic 02 job 02), 4 (timeouts for reasoning models: advice, no code)
      and 6 (whether to adopt vde's amended instructions). The agent does not send it.
- [ ] Supersede the matching rows of `docs/conformance/travelinbox-schema-suggestions.md`
      with a pointer to the new document.

## Acceptance criteria

- The examples use no value from any corpus document (checked by grepping the corpus
  for each example value).
- The comparison is recorded; each kept convention names the mismatch it removed.
- The hand-over document exists with measured numbers only, no document content.

## Out of scope

An `examples` request field in `/v1` (only if instruction examples prove worth it, in a
later version), brand-name lookup tables (TravelInbox's side).

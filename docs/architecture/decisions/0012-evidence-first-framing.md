# ADR 0012: Evidence-first framing, deviating from contract §6 and §7

Status: accepted, 2026-10-20 (owner decision) · Amends: [ADR 0005](0005-contract-first.md)
on the points below only

## Context

The TravelInbox contract fixes how the prompts are built: caller instructions, schema,
document (§6 steps 2 and 5), an extractor answer that is "exactly one JSON object … no
prose" (§6 step 2), and a verdict object that is the judge's "whole answer" (§7). v1.2
(epic [01](../../roadmap/v1.2-extraction-quality/01-prompts-and-preparation/README.md))
changes three of these, on published evidence that models answer long-document questions
better with the document first and the task last, and that reasoning freely before a
fixed-format answer recovers most of the accuracy the format instruction costs.

ADR 0005 makes the contract the acceptance test: the service may be a superset, never a
deviation. These changes cannot be a superset, because the prompt has one order and the
answer one format, and a per-application switch would give two framings to maintain and
measure.

## Decision

One framing for every application, the default from v1.2:

1. **Document first** (job 01): both roles get the document, then the schema, then the
   caller's instructions, then the previous candidate and feedback (extractor) or the
   candidate (judge). Caller instructions stay verbatim and complete (§6 is otherwise
   unchanged).
2. **Extractor analysis block** (job 05): the extractor answers with a short
   `<analysis>` block, then the JSON inside `<json>…</json>`. The service parses only the
   `<json>` block when there is one and falls back to the §6 step 3 parsing otherwise. The
   JSON is still never repaired.
3. **Judge analysis block** (job 06): the judge answers with an `<analysis>` block, then
   the §7 verdict object inside `<verdict>…</verdict>`. The verdict object, its keys and
   their meaning are unchanged; a missing, unclosed or invalid verdict block is an
   unparseable verdict, never acceptance.

Analysis text is discarded after parsing: never stored, logged, returned, put into
feedback or sent back as the previous candidate.

ADR 0005 is amended for these three points: the service deviates from contract §6 steps
2 and 5 and from §7 ("the judge's whole answer"), and the owner proposes the amended
wording to TravelInbox ([proposal](../../conformance/contract-amendment-v1.2.md)). The
traceability table ([travelinbox-v1.md](../../conformance/travelinbox-v1.md)) marks the
affected rows "deviation, amendment proposed (ADR 0012)". Everything else in the contract
holds as before.

## Consequences

- `service_version` changes with each framing and parser change (contract §11), so
  TravelInbox's stored results stay attributable.
- TravelInbox's own instructions say "Return only the JSON object: no prose, no code
  fences". The framings say that such a line describes the content of the `<json>` or
  `<verdict>` block; a model that obeys the caller instead still parses, through the
  fallback. The amendment asks TravelInbox to drop the line.
- Each change is measured on the epic's core set and reverted if it is worse; a revert
  restores the contract wording for that point and updates this ADR.

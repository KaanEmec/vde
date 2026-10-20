# Job: Extractor analysis block

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: in progress · Depends on: 04

Implementation done; comparison pending (orchestrator).

## Goal

Let the extractor reason before it answers: a short `<analysis>` block that quotes the
document's evidence for each field it fills (and says which fields it found nothing
for), then the answer as JSON inside `<json>…</json>`. The parser reads only the `<json>`
block.

## Context

The framing asks for "exactly one JSON value and nothing else". Studies of the format tax
find that the format instruction, more than the decoding constraint, costs accuracy, and
that reasoning freely first recovers most of it. Quoting the evidence first is also
Anthropic's advice for long documents, and per-field evidence is what job 06 asks the
judge for.

`internal/extract/parse.go` (`parseJSON`) already tolerates prose and code fences around
one JSON value. The analysis costs output tokens: `max_tokens` per role and the
truncation path (`notJSON` with `truncated`) must leave room for it.

## Tasks

- [x] `framings/extractor.md`: the two-block answer format, the analysis kept short
      (one line per field: pointer, quote or "not in the document"), quotes copied
      exactly.
- [x] `parse.go`: when a `<json>` block is present, parse only its content; without one,
      fall back to today's parsing, so bare JSON (fake-provider scenarios, the classifier
      path, models that ignore the format) still works. A `<json>` block that is not
      valid JSON is a parse failure, as today. Decided: an `<analysis>` block must be
      closed and followed by a `<json>` block (no fallback once the answer uses blocks);
      two `<json>` blocks are refused, not "last wins" (`docs/operator/framings.md`).
- [x] The analysis text is discarded after parsing: never stored, logged, returned or
      put into a report. On a retry the previous candidate is the parsed JSON only.
- [ ] Decide the analysis budget: measure output tokens per case; raise the extractor's
      default `max_tokens` only if truncations appear, and say so in the CHANGELOG.
      (Default left at 16,384 pending the measurement; the cut-off feedback now asks for
      one short analysis line per value.)
- [x] Scenarios: one fake-provider scenario answering in the two-block format, one where
      the analysis contains a `{` that is not the answer, one cut off inside `<analysis>`.
- [ ] `docs/api/writing-instructions.md`: the answer format, so caller instructions do
      not contradict it ("answer with JSON only").
- [ ] Run the comparison, including tokens and cost per case, record it, keep or revert.

## Acceptance criteria

- `parse_test.go`, table-driven: tag present, tag absent, braces inside the analysis, two
  `<json>` blocks (the last one wins, or reject; decide and document), an unclosed tag.
- `FuzzParseJSON` still runs clean.
- No test output, log line or report contains analysis text (a test with a marker string
  in the analysis checks the logs and the outcome).
- `version_test.go`: `service_version` changes.
- The comparison is recorded with the keep or revert decision and the cost change.

## Out of scope

Provider reasoning modes (extended thinking, reasoning effort): configuration per
endpoint, measured separately if this job is kept. Native structured outputs (job 07).

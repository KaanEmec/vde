# Job: Judge evidence quotes

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: in progress · Depends on: 05

Implementation done; comparison pending (orchestrator).

## Goal

Let the judge check field by field before it decides: an `<analysis>` block that, for each
value it disputes, quotes what the document says, then the verdict JSON inside
`<verdict>…</verdict>`. Feedback messages carry the quote and the place (page, section
label from jobs 02 and 03), so the extractor's retry knows what to change.

## Context

`internal/extract/verdict.go` (`parseVerdict`) reads one JSON object with `verdict`,
`feedback` and `confidence`. The judge framing already allows quoting in messages, but
asks for "exactly one JSON object and nothing else". A failed or unparseable judge call is
never acceptance (ADR 0009); that stays.

The classifier judge (`internal/provider/classifier`) renders its own verdict JSON
without tags; it must keep working.

## Tasks

- [x] `framings/judge.md`: the two-block answer; the analysis checks each candidate value
      against the document, quoting it, and lists disputed pointers; each feedback message
      says what is wrong, quotes the document and names the page or section.
- [x] `verdict.go`: parse only the `<verdict>` block when present, else today's parsing.
      An unclosed or invalid `<verdict>` block is a parse failure, which is never
      acceptance.
- [x] Discard the analysis after parsing, as in job 05.
- [x] Scenarios: a two-block reject whose feedback the extractor fixes, a two-block
      accept, a reject with an analysis but no verdict block (must not be acceptance).
- [ ] `docs/api/writing-instructions.md`, "Verdict format".
- [ ] Run the comparison: judge rejects, attempts per case, cases fixed on retry, tokens
      and cost; record it, keep or revert.

## Acceptance criteria

- `verdict_test.go` and `FuzzParseVerdict`: tag present, absent, unclosed, braces in the
  analysis; a missing verdict block never yields `accept`.
- The classifier judge tests pass unchanged.
- `version_test.go`: `service_version` changes.
- The comparison is recorded with the keep or revert decision.

## Out of scope

A new verdict field in `/v1` (the analysis is never returned), judge confidence
calibration.

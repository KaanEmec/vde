# Job: Conventions in the schema

Version: v1.3 · Epic: 01 Conventions in schema and instructions · Status: done · Depends on: 01

## Goal

Turn the conventions that have a checkable form into schema constraints, so a value in the
wrong form fails validation and goes back to the extractor as schema feedback: free,
deterministic, before any judge call.

## Context

Schema violations are fed back to the extractor without a judge call (contract §6 step 4)
and cost one extractor retry. Of the production misses in TravelInbox's report, the
`details` ones have a form a pattern can check: coach and seats without their
abbreviations (`7`, not `Wg. 7`), terminal without its label (`1`, not `Terminal 1`),
cabin as the cabin class only (not with the tariff name). `travellers[].type` can be
constrained so a party count does not type a named traveller. Brand names (`provider`)
have no checkable form; they are job 03's.

`docs/conformance/travelinbox-schema-suggestions.md` already lists description and type
suggestions per pointer. The schema is shared through `$defs` (`$defs/text`,
`$defs/rail`, `$defs/flight`, `$defs/flight_leg`, `$defs/traveller`): a pattern goes on
the property, not on the shared `text` definition. Every `truth.json` of the corpus must
still validate, or the constraint contradicts TravelInbox's own expected answers.

## Tasks

- [x] The corpus `schema.json` is already a copy of TravelInbox's production schema (done
      in job 01, 2026-10-22); constraints go into that copy.
- [x] Add constraints with descriptions that state the convention: `pattern` on
      `$defs/rail` coach and seat, `$defs/flight` and `$defs/flight_leg` terminal, a
      cabin description and `enum` or `pattern` where the corpus's values allow it; the
      `travellers[].type` rule. Each constraint carries a `description` the schema
      feedback can quote.
- [x] Check that every corpus `truth.json` validates against the new copy (a small test
      in `internal/eval` or a `go test` over the corpus directory, whichever already
      exists for truth validation).
- [x] Bump the copy's `schema_version` marker if the schema has one, or note the version
      in the README.

### Constraints added (`testdata/travelinbox/corpus/schema.json`)

Each is on the property, not on `$defs/text`; the pattern is the narrowest form every
`truth.json` satisfies.

| Pointer | Constraint | Miss targeted |
| --- | --- | --- |
| `/$defs/rail/properties/coach` | `pattern` (alphanumeric, 1 to 4 characters) and description | coach with its label |
| `/$defs/rail/properties/seat` | `pattern` (designators, `, `-separated) and description | seat with its label |
| `/$defs/flight/properties/seat`, `/$defs/flight_leg/properties/seat` | same `pattern`; description | seat with its label |
| `/$defs/flight/properties/terminal`, `/$defs/flight_leg/properties/terminal` | `pattern` (alphanumeric, 1 to 5 characters) and description | terminal with its label |
| `/$defs/flight/properties/cabin`, `/$defs/flight_leg/properties/cabin` | description only (the corpus's cabin values are free text, no pattern fits) | cabin with the tariff name |
| `/$defs/traveller/properties/type` | description only (the `enum` already exists) | `type` set from a party count |

Validation: a throwaway check over the schema with the repository's JSON Schema library
(not added to the repository) validates all 50 corpus `truth.json` files; a label added to a
coach is rejected. The `schema_version` marker is the contract's data version (`const 1`)
and is not bumped; the proposal is noted in `testdata/travelinbox/README.md`. `make eval`
(fake provider) passes.

- [x] Run the comparison against job 01's report; record it with schema retries per case
      (attempts) and the `details` and `travellers` mismatches; keep or revert each
      constraint by its pointer.

Result (2026-10-22): 20 → 14 mismatches, convention 11 → 5, all five `details` misses gone;
every constraint kept. See [quality](../../../quality.md#v13-convention-aware-verification-2026-10-22).

## Acceptance criteria

- Every corpus `truth.json` validates against the new copy (test).
- The comparison is recorded; each kept constraint names the mismatch it removed.
- `make check` and `make eval` pass; conformance unchanged (it reads `FromTravelApp/`).

## Out of scope

Grounding annotations (epic 02 job 01), brand names, descriptions not tied to a measured
miss (they stay in the suggestions document).

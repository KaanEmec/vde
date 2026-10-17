# Job: `Check` and schema annotations

Version: v0.4 · Epic: 02 Grounding check · Status: planned · Depends on: 01

## Goal

`ground.Check(docText, candidate, schema, opts)` walks a candidate and returns one feedback
entry per ungrounded leaf, honouring the schema's enums and `x-vde-grounding` annotations.

## Context

ADR 0009. Callers mark properties `derived` (an allowed derivation such as city, country,
IATA code or currency, not checked), `skip` (never checked, for example notes, titles or
confidence) or `critical` (must be grounded for the `auto` gate). When no property is marked
`critical`, every checked leaf is critical. JSON Schema 2020-12 ignores unknown keywords, so
schemas carrying the annotation stay valid for every validator, TravelInbox's included.

## Tasks

- [ ] `internal/ground/ground.go`:
      `Check(doc *Text, candidate json.RawMessage, rules Rules, opts Options) Result`, where
      `Result{Ungrounded []Miss; Checked, Skipped, CriticalMissed int; Ungroundable bool}`
      and `Miss{Pointer string; Critical bool}`. A nil `doc` gives `Ungroundable: true` and
      no misses. `internal/ground` does not import `internal/extract` (which imports it),
      so `Rules` is a function type, `func(location []string) (rule Rule, enum []string)`,
      and `extract` supplies it from the compiled schema. `extract` turns each miss into a
      `Feedback` with `source: "grounding"`, the leaf's RFC 6901 pointer and the fixed
      message `value not found in the document`. The message never quotes the value, so it
      is safe wherever feedback goes.
- [ ] Leaves: strings (after trimming; empty strings skipped), numbers (via job 01),
      ISO dates and datetimes (via job 01; other strings are matched as normalized
      substrings on token boundaries). Booleans and nulls are skipped. A value equal to one
      of the schema's `enum` or `const` values at that location is skipped. Strings shorter
      than `opts.MinLength` (default 2) are skipped.
- [ ] Annotations: `internal/extract/schema.go` gains `(*Schema).GroundingRules()
      ground.Rules`, which reads `x-vde-grounding` and `enum`/`const` from the raw
      subschema at each leaf's location (reusing `subschema` and `enumValues`),
      resolving through `$ref`, `items`, `prefixItems` and `properties`. An annotation on
      an object or array applies to every leaf below it unless a leaf overrides it. An
      unknown `x-vde-grounding` value makes `CompileSchema` fail (`invalid_schema`).
- [ ] Schema-less mode: a nil `Rules`; every leaf is checked and critical.
      `Options.MaxMisses` caps the entries (the loop's feedback cap applies as well).
- [ ] Synthetic fixture `internal/ground/testdata/event-invoice/`: an invoice text written
      for the test (a supplier, line items, an amount, an invoice date, no event details)
      and a candidate with invented event dates, a venue, an organiser and travellers next
      to correct invoice fields. The schema marks `notes` as `skip` and `currency` as
      `derived`. The test lives in package `ground_test`, so it can compile the schema with
      `extract.CompileSchema` without an import cycle.

## Acceptance criteria

- Table tests with planted invented values: each invented leaf yields exactly one entry with
  the right pointer. Correct values printed in another format (number, date, case,
  diacritics) yield none.
- The event-invoice test flags the event dates, venue, organiser and every traveller name,
  and nothing else. `CriticalMissed > 0`.
- Annotation tests: `skip` and `derived` leaves are counted as skipped. With one `critical`
  property, misses elsewhere are reported but do not count in `CriticalMissed`. An
  annotation on an array applies to its items.
- No test output or error carries a candidate value (an assertion like the redaction tests
  in `internal/logging`).

## Out of scope

Verifying derivations (for example that an IATA code matches a printed city), cross-field
consistency checks, model-assisted grounding.

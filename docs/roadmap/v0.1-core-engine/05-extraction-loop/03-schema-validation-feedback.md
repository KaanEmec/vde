# Job: Schema validation as feedback

Version: v0.1 · Epic: 05 Extraction loop · Status: planned · Depends on: 02

## Goal

Compile the caller's JSON Schema 2020-12 once per job, validate each candidate, and turn
violations into feedback entries with JSON pointers and human-readable messages that the
extractor can act on.

## Context

Contract §4 (`422 invalid_schema` when the schema part is not valid 2020-12), §5
(`feedback` entries with `pointer`, `message`, `source: "schema"`; `/reservations/-` style
pointers for something missing from an array), §6 step 4 (one entry per violation,
pointer to the offending location, message naming the keyword and the expected form; no
judge call). The TravelInbox schema uses `$defs`, `oneOf`, `if/then`, `propertyNames`,
`const`, patterns and `additionalProperties: false`, so the validator must be complete.

## Tasks

- [ ] Use `github.com/santhosh-tekuri/jsonschema/v6` with 2020-12 as default draft, no
      remote `$ref` resolution (refuse remote refs with `invalid_schema`), format
      assertions enabled for `date-time` and the formats the library ships.
- [ ] `CompileSchema(bytes) (*Schema, error)`: used at submit for `invalid_schema` and
      cached on the job for the loop.
- [ ] `Validate(schema, candidate) []Feedback`: flatten the validator's error tree into
      leaf violations; `pointer` from the instance location; `message` like
      "`starts_at_local`: does not match pattern ^[0-9]{4}-...; expected a local
      date-time YYYY-MM-DDTHH:MM:SS" using the keyword and schema `description` when
      present; for `required` emit one entry per missing property pointing to the parent
      with the property name in the message; cap entries (default 50) with a final
      summary entry.
- [ ] `oneOf` failures: report the branch errors of the branch matching a discriminator
      (`const` on a property) when one branch's `const` matches, else a single entry on
      the parent; keep this heuristic small.
- [ ] Tests with the TravelInbox schema and ground-truth results from
      `testdata/travelinbox/` (decision 11): a valid result, a missing confidence for a present
      field (the `allOf`/`if` rule), a wrong pattern, an unknown property, a wrong `kind`
      details shape.

## Acceptance criteria

- Every violation in the tests yields a pointer into the candidate and a message that
  names the keyword and the expected form.
- A schema with a remote `$ref` is refused as `invalid_schema`.

## Out of scope

Schema repair or inference; validating the schema against a meta-schema beyond what the
library does.

# Job: Schema-less mode

Version: v0.1 · Epic: 05 Extraction loop · Status: planned · Depends on: 05

## Goal

Extraction without a predefined schema, where the caller's instructions describe the JSON
structure and the judge checks accuracy and completeness against the document and the
instructions.

## Context

Brief: "Without a predefined domain schema, instructions guide the JSON structure" and
"Demonstrate extraction with and without a schema." The TravelInbox contract always sends
a schema; the service is a superset (ADR 0005, decision 6).

## Tasks

- [ ] `Input.Schema == nil` path in the loop: skip schema validation; the answer parser
      accepts a JSON object or array at top level; the extractor framing says "shape the
      JSON as the instructions describe" instead of "conform to the schema".
- [ ] Judge framing in this mode asks to check against the instructions as well as the
      document (fields the instructions asked for but the candidate lacks are
      completeness issues).
- [ ] The HTTP API (v0.2) accepts a missing `schema` part; the TravelInbox OpenAPI
      superset marks it optional with a note.
- [ ] Scenario tests with the fake provider; CLI example in epic 06.

## Acceptance criteria

- `vde extract` without `--schema` returns an accepted or rejected JSON result.
- With a schema, behaviour is unchanged (regression tests).

## Out of scope

Inferring a schema from a result, returning a generated schema.

# Job: One configuration validator

Version: v1.1 · Epic: 03 Single source · Status: planned · Depends on: 01-01; owner decision L2, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

## Goal

`vde config check` validates with the same code as `vde serve` (`config.Validate`), not
additionally with `schema.json`; `schema.json` stays what `vde config schema` prints and
what tests compare with the structs. This changes `config check` output for files that
only the schema refuses, so it needed the owner's agreement (L2, given 2026-10-19).

## Context

- `internal/config/schema.go` (232 lines: `CheckSchema`, `collectViolations`,
  `normalizeYAML`, `schemaPath`, `schemaDescription`) and `cmd/vde/config.go`
  `schemaViolations`: `config check` can fail on a file that `serve` accepts.
- `Parse` already refuses unknown keys (`KnownFields(true)`); ranges and enums are
  written in the validators and again in `schema.json` (16 enums, 23 minimums, 10
  maximums, 5 exclusive minimums, 4 patterns).
- `TestSchemaMatchesStructs` and `TestExamplesMatchSchema` keep the schema honest and stay
  (the jsonschema dependency stays for caller schemas).
- About −190 production and −150 test lines (`TestCheckSchemaViolations`,
  `TestCheckSchemaInput`).

## Tasks

- [ ] Prove the schema catches nothing that `Validate` misses.
  - Skill: EVID.
  - Prompt: "Run every case of TestCheckSchemaViolations and TestCheckSchemaInput through
    config.Parse plus Validate only; list the cases that Validate accepts but the schema
    refuses. Report only."
- [ ] Close the gaps found, in `Validate`.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: for each case the schema refuses and
    Validate accepts, add the check to the validators in internal/config with a test.
    Guardrails: skills.md."
- [ ] Remove the runtime schema check.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/config/schema.go cmd/vde/config.go — remove CheckSchema
    and its helpers and the schemaViolations path of `config check`; keep `vde config
    schema`, schema.json, TestSchemaMatchesStructs and TestExamplesMatchSchema. Update
    docs/operator/configuration.md ('Schema') and CHANGELOG. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- Every former schema-violation case is refused by `config check` and by `serve` with the
  same message; `make check` green.

## Out of scope

- Generating `configuration.md` from the schema (08-02).

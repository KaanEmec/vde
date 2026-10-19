# Job: One configuration validator

Version: v1.1 · Epic: 03 Single source · Status: done (Gate task left to the orchestrator) · Depends on: 01-01; owner decision L2, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

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

- [x] Prove the schema catches nothing that `Validate` misses.
  - Skill: EVID.
  - Prompt: "Run every case of TestCheckSchemaViolations and TestCheckSchemaInput through
    config.Parse plus Validate only; list the cases that Validate accepts but the schema
    refuses. Report only."
- [x] Close the gaps found, in `Validate`.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: for each case the schema refuses and
    Validate accepts, add the check to the validators in internal/config with a test.
    Guardrails: skills.md."
- [x] Remove the runtime schema check.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/config/schema.go cmd/vde/config.go — remove CheckSchema
    and its helpers and the schemaViolations path of `config check`; keep `vde config
    schema`, schema.json, TestSchemaMatchesStructs and TestExamplesMatchSchema. Update
    docs/operator/configuration.md ('Schema') and CHANGELOG. Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- Every former schema-violation case is refused by `config check` and by `serve` with the
  same message; `make check` green.

## Out of scope

- Generating `configuration.md` from the schema (08-02).

## Result

Evidence (a throwaway test, not committed): every case of `TestCheckSchemaViolations` and
`TestCheckSchemaInput` was already refused by `Parse` or `Validate`. A wider sweep, one
violating value per schema constraint (16 enums, the minimums, maximums, exclusive
minimums and patterns, top level, lists and maps), found three gaps, and the CLI test
`TestRunConfigSchema` a fourth:

- `limits.feedback_message_chars` negative (the loop read it as the default);
- `storage.max_open_conns` outside 0 to 1000 with `sqlite` (checked only for postgres);
- `blobs.sse` other than `AES256` or `""` with `fs` (checked only for s3);
- a non-string scalar where a string is expected (`name: 42`, `accepts: [text/plain,
  true]`): yaml.v3 read it as `"42"`.

Closed, each with a failing test first: the three range and enum checks in `Validate`
(`limits`, `storage`); the type check in `Parse` (`stringScalars`, walking the YAML node
tree beside the struct types, `config: line N: <key>: got number, want string`). These
four now refuse in `vde serve` too (owner decision L2; CHANGELOG, "Changed").

Removed: `CheckSchema`, `collectViolations`, `normalizeYAML`, `schemaPath`,
`schemaDescription`, `cmpIssue`, the compiled schema and the CLI's `schemaViolations`
pass; `config check` help text and `docs/operator/configuration.md` ("Schema") say it
validates as `serve` does. Kept: `vde config schema`, `schema.json`,
`TestSchemaMatchesStructs`, `TestDocumentedKeysAreInSchema` and `TestExamplesMatchSchema`
(which now compiles the schema itself). `TestCheckSchemaViolations` became
`TestFormerSchemaViolations` (each case, the four gaps included, refused by Parse or
Validate at its key); `TestCheckSchemaInput` went, its accepted cases moved there.

Checks: `make check`, `make scenarios` and `make ci` green. The first `make ci` run hit a
sampling flake in `TestPDFPreparedByService` (5 % default rate keyed by the random job
id), fixed in its own commit before this one.

`git diff --stat` (Go): production 5 files, +90 −260 (net −170); tests 5 files, +123 −81
(net +42: the gap tests and the test-side schema compile).

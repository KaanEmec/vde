# Job: Compatibility policy and `/v1` freeze

Version: v1.0 · Epic: 01 API freeze · Status: done · Depends on: v0.2 09-03

## Goal

Write down what stability means and enforce it with tests.

## Context

Contract §11: additive changes only within `/v1`; a breaking change ships as `/v2` with
`/v1` available for at least 12 months.

## Tasks

- [x] `docs/compatibility.md`: what is frozen (paths, fields, types, status and error
      code meanings, headers, multipart part names, limits as minimums), what may change
      (additions, defaults flagged as such), deprecation process, `/v2` policy.
- [x] Snapshot tests: the OpenAPI document at `v1.0.0` is committed as
      `api/openapi.v1.0.0.yaml`; a test asserts the current document is a superset.
- [x] Review every `MAY` in the contract that was implemented and mark it stable or
      experimental in the API document.
- [x] Freeze the v0.4 supersets with the rest: the `judge` request part (and JSON field)
      with its `400` when not allowed, the job field `verification` (`judge`, `grounded`,
      `none`, `null` while in progress), the terminal status `extracted`, the feedback
      source `grounding` with its fixed message, and the schema annotation
      `x-vde-grounding` (`critical`, `check`, `derived`, `skip`), including what an unknown
      value answers (`422 invalid_schema`). Decide whether the grounding rules (date table,
      normalization) are part of the promise or versioned only through `service_version`.
      Decided: versioned only through `service_version` (rules version `ground-2`); the
      entry shape (source `grounding`, fixed message, no value) is promised.

## Acceptance criteria

- The policy is linked from the README and the API guide.

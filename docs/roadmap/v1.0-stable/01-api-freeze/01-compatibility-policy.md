# Job: Compatibility policy and `/v1` freeze

Version: v1.0 · Epic: 01 API freeze · Status: planned · Depends on: v0.2 09-03

## Goal

Write down what stability means and enforce it with tests.

## Context

Contract §11: additive changes only within `/v1`; a breaking change ships as `/v2` with
`/v1` available for at least 12 months.

## Tasks

- [ ] `docs/compatibility.md`: what is frozen (paths, fields, types, status and error
      code meanings, headers, multipart part names, limits as minimums), what may change
      (additions, defaults flagged as such), deprecation process, `/v2` policy.
- [ ] Snapshot tests: the OpenAPI document at `v1.0.0` is committed as
      `api/openapi.v1.0.0.yaml`; a test asserts the current document is a superset.
- [ ] Review every `MAY` in the contract that was implemented and mark it stable or
      experimental in the API document.

## Acceptance criteria

- The policy is linked from the README and the API guide.

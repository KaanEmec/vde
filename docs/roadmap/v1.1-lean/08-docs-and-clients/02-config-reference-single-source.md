# Job: Configuration reference from one source

Version: v1.1 · Epic: 08 Docs and clients · Status: planned · Depends on: 03-06

## Goal

Each configuration key is described in one place. Today it is described three times: the
struct, `schema.json` (143 `description` entries) and `docs/operator/configuration.md`
(266 lines), kept in step by three tests.

## Context

- Tests: `TestEveryKeyIsDocumented`, `TestDocumentedKeysAreInSchema`,
  `TestSchemaMatchesStructs`.
- Option A: generate the key table of `configuration.md` from the `schema.json`
  descriptions (a test that rewrites the table with `-update` and fails when it is
  stale), replacing the two doc tests. Adds about 60 lines of test code, removes about
  100 to 150 doc lines.
- Option B: keep `configuration.md` as the description and reduce the schema
  descriptions to one short line each (editor hints); keep the tests.
- Pick the option with less total text to maintain; prefer B if A's generator is longer
  than the doc tests it replaces.

## Tasks

- [ ] Compare the options.
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens): compare option A and
    option B of docs/roadmap/v1.1-lean/08-docs-and-clients/02-config-reference-single-source.md
    by lines added and removed and by how a new key is documented afterwards. Recommend
    one."
- [ ] Execute the chosen option.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/config docs/operator/configuration.md — apply option
    <A|B>; every key stays documented and the docs tests stay meaningful. Guardrails:
    skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`.

## Acceptance criteria

- One description per key; `make check` green; adding a key in a test branch fails
  until it is documented once.

## Out of scope

- Changing keys or defaults.

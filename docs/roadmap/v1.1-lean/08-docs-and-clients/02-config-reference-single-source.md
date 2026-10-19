# Job: Configuration reference from one source

Version: v1.1 · Epic: 08 Docs and clients · Status: done · Depends on: 03-06

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

- [x] Compare the options (measured, no CRITIC agent; see Result).
  - Skill: CRITIC.
  - Prompt: "Use the architecture-critic agent (or the pasted lens): compare option A and
    option B of docs/roadmap/v1.1-lean/08-docs-and-clients/02-config-reference-single-source.md
    by lines added and removed and by how a new key is documented afterwards. Recommend
    one."
- [x] Execute the chosen option (B).
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/config docs/operator/configuration.md — apply option
    <A|B>; every key stays documented and the docs tests stay meaningful. Guardrails:
    skills.md."
- [x] Gate (`make check`).
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`.

## Acceptance criteria

- One description per key; `make check` green; adding a key in a test branch fails
  until it is documented once.

## Out of scope

- Changing keys or defaults.

## Result

Option B, by measurement. The schema's 143 descriptions were already one line each
(6,833 characters, median 43); the reference has 95 key rows whose Meaning cells hold
17,982 characters with links and cross-key rules. Option A would move those cells into
JSON strings (markdown in editor hovers) and add a Type and a Default field per key plus a
generator of about 80 lines, to replace two doc tests of 112 lines: more text to maintain,
not less. Option B: every hint is at most 80 characters with no default (the reference's
Default column is their one home; 41 defaults and one stale statement, `debug.log_content`
"refused with a non-loopback listen", removed), 5,522 characters left. The three tests
stay; a new `TestSchemaDescriptionsAreHints` (24 lines) keeps hints from growing into a
second reference. A new key fails `TestSchemaMatchesStructs` until it is in the schema and
`TestEveryKeyIsDocumented` until `configuration.md` describes it. `git diff --stat`:
4 files, +87 −61 (schema.json 118 lines rewritten in place).

# Job: Deduplicate and de-stale the documentation

Version: v1.1 · Epic: 08 Docs and clients · Status: planned · Depends on: epics 01 to 07 (they edit docs); 08-04 decided

## Goal

Each fact has one home and the others link to it; no document states something the code
no longer does. About −150 to −250 lines.

## Context

Duplicated content and its proposed home:

| Content | Copies today | Home |
| --- | --- | --- |
| Working agreement, commit per job | AGENTS "Conventions"; CONTRIBUTING "Working agreement"; roadmap README "Working agreement for implementation"; overview "Working agreement while building autonomously" (historical) | AGENTS; CONTRIBUTING points to it |
| Design principles | AGENTS table; overview "Design principles"; CONTRIBUTING "Code"; roadmap README "Guiding constraints" | overview |
| Package map | AGENTS "Repository map"; overview "Packages" | one of them (AGENTS says it "does not copy sources") |
| `make` commands | AGENTS; README "Development"; CONTRIBUTING; STATUS | AGENTS; README keeps a three-line pointer |
| Project status | README "Status and roadmap" (stale: "v0.1 to v0.4"); STATUS; roadmap README status line; CHANGELOG | STATUS |
| Known gaps | STATUS; CHANGELOG per release (×5); backlog; open questions | STATUS and backlog; CHANGELOG keeps release-time gaps |
| Declaring `accepts` | `docs/providers/README.md`; `docs/operator/providers.md` | `docs/operator/providers.md` |
| Binary size | STATUS, CHANGELOG, `docs/operator/pdf.md`, Dockerfile comment | `docs/operator/pdf.md` |

Stale statements: `docs/operator/README.md` "What is planned" (`--probe` "planned for
v0.3", gone after 01-02); `docs/operator/providers.md` "the service never renders pages"
(false since v0.4); CONTRIBUTING "until a remote repository exists" and its required
checks list (missing `fuzz`, `conformance`, `eval`, `storage`); seven roadmap jobs left
`in progress` inside done versions (v0.1 02-04; v0.2 06-03, 09-03, 10-01, 10-02, 10-03;
v0.3 02-01): set each to `done` or `dropped` with a one-line reason. `docs/quality.md`
names two reports without links.

## Tasks

- [ ] Re-check every row against the tree as it is after the code epics.
  - Skill: none (Explore agent, read-only).
  - Prompt: "Read-only: for each row of the table in
    docs/roadmap/v1.1-lean/08-docs-and-clients/01-dedupe-and-destale.md and each stale
    statement, confirm it still holds and give file:line. Also list any statement in
    README.md, AGENTS.md, CONTRIBUTING.md, docs/operator/*.md and docs/providers/*.md
    that names a symbol, flag, file or command that no longer exists (grep each
    against the tree). Report a table only."
- [ ] Deduplicate.
  - Skill: none (Edit; no skill consolidates docs reliably, see skills.md).
  - Prompt: "Apply the table: keep the content in its home, replace each other copy with
    a one-line link. Targeted edits, no rewrites (AGENTS.md: shared files get targeted
    edits). Keep the facts docs tests read. Run `make check` after each file. Never print
    document content or keys. Guardrails: skills.md."
- [ ] De-stale, including the seven roadmap statuses and their epic tables.
  - Skill: none (Edit).
  - Prompt: "Fix each stale statement found; set the seven roadmap jobs to done or
    dropped with a one-line reason in the job file and the epic table; link the two
    unlinked quality reports. Guardrails: skills.md."
- [ ] Check AGENTS.md is still a pointer file.
  - Skill: none (read-only agent).
  - Prompt: "Read AGENTS.md as a new agent would: list instructions that conflict with
    each other or with CONTRIBUTING.md, and copies of content that has a home elsewhere.
    Report only."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review low <first>^..<last>` (links and doc tests), then `make check`.

## Acceptance criteria

- Each row of the table has one home; no dead link (`grep -o '](.*\.md[^)]*)'` resolved);
  docs tests green; docs lines down by at least 150.

## Out of scope

- ADRs (never rewritten; amend only), CHANGELOG entries of past releases.

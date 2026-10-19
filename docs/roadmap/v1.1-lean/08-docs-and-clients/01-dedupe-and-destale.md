# Job: Deduplicate and de-stale the documentation

Version: v1.1 · Epic: 08 Docs and clients · Status: done · Depends on: epics 01 to 07 (they edit docs); 08-04 decided

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
(false since v0.4); CONTRIBUTING "until a remote repository exists" (its CI and release
sections are rewritten by 00-02 and 00-03); seven roadmap jobs left
`in progress` inside done versions (v0.1 02-04; v0.2 06-03, 09-03, 10-01, 10-02, 10-03;
v0.3 02-01): set each to `done` or `dropped` with a one-line reason. `docs/quality.md`
names two reports without links.

## Tasks

- [x] Re-check every row against the tree as it is after the code epics (by hand, no agent).
  - Skill: none (Explore agent, read-only).
  - Prompt: "Read-only: for each row of the table in
    docs/roadmap/v1.1-lean/08-docs-and-clients/01-dedupe-and-destale.md and each stale
    statement, confirm it still holds and give file:line. Also list any statement in
    README.md, AGENTS.md, CONTRIBUTING.md, docs/operator/*.md and docs/providers/*.md
    that names a symbol, flag, file or command that no longer exists (grep each
    against the tree). Report a table only."
- [x] Deduplicate.
  - Skill: none (Edit; no skill consolidates docs reliably, see skills.md).
  - Prompt: "Apply the table: keep the content in its home, replace each other copy with
    a one-line link. Targeted edits, no rewrites (AGENTS.md: shared files get targeted
    edits). Keep the facts docs tests read. Run `make check` after each file. Never print
    document content or keys. Guardrails: skills.md."
- [x] De-stale, including the seven roadmap statuses and their epic tables.
  - Skill: none (Edit).
  - Prompt: "Fix each stale statement found; set the seven roadmap jobs to done or
    dropped with a one-line reason in the job file and the epic table; link the two
    unlinked quality reports. Guardrails: skills.md."
- [x] Check AGENTS.md is still a pointer file.
  - Skill: none (read-only agent).
  - Prompt: "Read AGENTS.md as a new agent would: list instructions that conflict with
    each other or with CONTRIBUTING.md, and copies of content that has a home elsewhere.
    Report only."
- [x] Gate (`make check`; every relative link and anchor resolves, checked by script; no
      `/code-review`).
  - Skill: REVIEW.
  - Prompt: `/code-review low <first>^..<last>` (links and doc tests), then `make check`.

## Acceptance criteria

- Each row of the table has one home; no dead link (`grep -o '](.*\.md[^)]*)'` resolved);
  docs tests green; docs lines down by at least 150.

## Out of scope

- ADRs (never rewritten; amend only), CHANGELOG entries of past releases.

## Result

Re-check: every row still held after the code epics except that 01-02 had already fixed
`docs/operator/README.md`'s `--probe` line (the section was left saying nothing is
missing). Homes applied:

- **Working agreement**: AGENTS "Conventions" (now "one commit on `main`", not "branch
  and PR once the remote is used"); CONTRIBUTING, the roadmap README and the overview point
  to it (the overview's historical section is gone).
- **Design principles**: the overview; CONTRIBUTING "Code" and the roadmap README "Guiding
  constraints" became links. AGENTS keeps its table: each row names where the principle is
  decided, which the overview does not, so it is an index rather than a copy.
- **Package map**: AGENTS "Repository map" (the more complete and current one; `config
  schema` added); the overview's code block became a link.
- **`make` commands**: AGENTS; README "Development" keeps `make check` and `make ci`;
  CONTRIBUTING "Checks" keeps the procedure (hooks, prerequisites) and links the list.
- **Status**: STATUS; README "Status and roadmap", the roadmap README status line and the
  overview status header became links (the README keeps the not-production-ready warning).
- **Known gaps**: STATUS and backlog; the partial-date and `auto`-threshold items live in
  the backlog only; the resolved "Open" questions are gone.
- **Declaring `accepts`**: `docs/operator/providers.md`; the providers README links it.
- **Binary size**: `docs/operator/pdf.md`; STATUS and the Dockerfile comment link it.

De-staled: "the service never renders pages" (providers README, operator providers,
troubleshooting); `--probe` (examples README, STATUS); `debug.log_content` "refused unless
loopback" (privacy) and its "switch" (operator README); Node and Swift (README, API guide);
"no key is deprecated" (configuration); the deferred release drafts target the removed
GitHub workflow (backlog); STATUS rewritten for v1.1 (done, verified, owner actions). The
seven statuses, in the history files with a one-line reason each: v0.1 02-04, v0.3 02-01,
v0.2 09-03, 10-01, 10-02, 10-03 set to `done` (06-03 was already `dropped` by 01-02), and
the epic tables and version tables with them. `docs/quality.md` links all three model
comparison reports.

AGENTS.md read as a new agent: a pointer file; one conflict left for the owner: "never
build to `bin/vde` while a `vde serve` from it may run" against `make check`, which builds
`bin/vde` and must pass before every commit.

`git diff --stat` without the v1.1 roadmap: 23 files, +157 −283 (markdown −125 net, STATUS
+12 of it). Below the −150 target, because STATUS grew to cover v1.1 and the AGENTS table
stayed; with 08-04, the docs are −4,636 lines.

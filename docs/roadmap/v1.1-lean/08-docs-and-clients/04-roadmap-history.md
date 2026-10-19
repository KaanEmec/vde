# Job: History of the done versions

Version: v1.1 · Epic: 08 Docs and clients · Status: planned · Depends on: owner decision L6, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

## Goal

The roadmap of the done versions is as small as the owner wants it, without losing the
record of what was decided and delivered.

## Context

- v0.1 to v1.0: 136 files, 6,661 lines (v0.1 35/1,640; v0.2 45/2,222; v0.3 22/996;
  v0.4 21/1,504; v1.0 13/299); 98 job files.
- No Go code or test reads them. Links from outside the roadmap: STATUS (2),
  `docs/security.md` (2), `docs/conformance/travelinbox-v1.md` (1). The owner's progress
  dashboard is generated from the `Status:` headers and epic tables (AGENTS.md,
  "Orchestration notes").
- Options: (a) keep everything (only 08-01's status fixes); (b) move done versions under
  `docs/roadmap/archive/` (navigation only); (c) collapse each done version into one
  `vX.Y-history.md` keeping, per job, the header line, Goal, acceptance criteria and the
  commit, about −4,000 lines, full text in git under tags `v0.3.0`, `v0.4.0`, `v1.0.0`;
  (d) delete job files and keep the epic tables. Default: (a), or (c) if the dashboard
  generator can read the new format. **Decided (L6): (c).**

## Tasks

- [x] Owner decision L6 (2026-10-19): collapse each done version into a history file.
- [ ] Check that the generator of the owner's progress dashboard reads the history
      format; adapt the format (not the generator) if it does not.
  - Skill: none (read-only agent).
  - Prompt: "Read the owner's dashboard artifact (its URL is in the owner's notes) and
    describe what it parses from docs/roadmap: file paths, header lines, tables. Report
    which history-file layout it can read unchanged."
- [ ] Collapse one version first, review, then the rest.
  - Skill: none (Edit, one agent per version file, disjoint files).
  - Prompt: "Collapse docs/roadmap/<version>/ into docs/roadmap/<version>-history.md:
    for each epic, its table; for each job, the header line, Goal (one paragraph),
    acceptance criteria and the commit hash from `git log --grep '<version>/<NN>'`.
    Keep the exit criterion check sections verbatim. Repoint every link (grep for the
    folder name). Never print document content. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review low <first>^..<last>`; open every repointed link.

## Acceptance criteria

- The decision is recorded; no dead link; the dashboard still renders every version.

## Out of scope

- The v1.1 roadmap itself.

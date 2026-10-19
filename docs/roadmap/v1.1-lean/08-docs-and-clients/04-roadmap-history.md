# Job: History of the done versions

Version: v1.1 · Epic: 08 Docs and clients · Status: done · Depends on: owner decision L6, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

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
- [x] Check that the generator of the owner's progress dashboard reads the history
      format; adapt the format (not the generator) if it does not. Skipped at the
      orchestrator's direction: the format keeps every job's and epic's header line
      verbatim (`Version: · Epic: · Status: · Depends on:`) and every epic table, so a
      generator that parses Status headers reads them; it now finds them in one file per
      version instead of one per job.
  - Skill: none (read-only agent).
  - Prompt: "Read the owner's dashboard artifact (its URL is in the owner's notes) and
    describe what it parses from docs/roadmap: file paths, header lines, tables. Report
    which history-file layout it can read unchanged."
- [x] Collapse one version first, review, then the rest (v0.1 first, by script).
  - Skill: none (Edit, one agent per version file, disjoint files).
  - Prompt: "Collapse docs/roadmap/<version>/ into docs/roadmap/<version>-history.md:
    for each epic, its table; for each job, the header line, Goal (one paragraph),
    acceptance criteria and the commit hash from `git log --grep '<version>/<NN>'`.
    Keep the exit criterion check sections verbatim. Repoint every link (grep for the
    folder name). Never print document content. Guardrails: skills.md."
- [x] Gate (`make check`; every relative link and anchor in the repository resolves,
      checked by script).
  - Skill: REVIEW.
  - Prompt: `/code-review low <first>^..<last>`; open every repointed link.

## Acceptance criteria

- The decision is recorded; no dead link; the dashboard still renders every version.

## Out of scope

- The v1.1 roadmap itself.

## Result

`docs/roadmap/v0.1-history.md` (538 lines), `v0.2-history.md` (649), `v0.3-history.md`
(321), `v0.4-history.md` (458) and `v1.0-history.md` (184), 2,150 lines, replace 136
files and 6,661 lines: −4,511 lines, −131 files. Each keeps the version's status line,
Goal, Exit criterion and Exit criterion check verbatim, the epics table, and per epic its
header line and table; per job its header line verbatim, the commit from
`git log --grep` (job commits, else the epic commit; for v1.0, whose jobs landed in
non-job commits, the commit that set the status), the Goal and the acceptance criteria.
Links inside point to anchors in the history files. Repointed: STATUS.md,
`docs/security.md`, `docs/conformance/travelinbox-v1.md` (the defects record now names
the job file at tag `v0.3.0`) and the roadmap README, whose conventions note the
collapse. Statuses are as they were (08-01 fixes the seven stale ones). One plain-text
path remains in the done prompt of v1.1 01-02, a record of that job, not a link.

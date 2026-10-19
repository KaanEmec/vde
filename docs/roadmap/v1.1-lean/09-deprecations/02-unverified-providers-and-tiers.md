# Job: Unverified providers and model tiers

Version: v1.1 · Epic: 09 Deprecations · Status: done · Depends on: owner decision L8, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17); 04-02, 05-01

## Goal

Deprecate in 1.1, for removal in 2.0, what the owner chose (L8): escalation, the second
judge, the classifier judge and `feedback_fallback`. Bedrock and Vertex stay. No
behaviour change in 1.1: the items keep working and warn.

## Context

| Candidate | Size (prod / test) | Dependencies it alone brings | Notes |
| --- | --- | --- | --- |
| `provider: bedrock` (kept, L8) | 241 / 365 | `bedrockruntime`, eventstream | AWS SDK core stays for `blobs.driver: s3` |
| `provider: vertex` (kept, L8) | 418 / 450 (about half after 04-02) | `golang.org/x/oauth2`, `cloud.google.com/go/compute/metadata` | |
| `provider: classifier` (deprecated, L8) | 185 / 219 | none | The owner asked for a classifier judge (open question 14, "TypeSafe AI's Jev"); no vendor API was available to test |
| Escalation, `second_judge`, `feedback_fallback: llm` with classifiers (deprecated, L8) | about 180 in `internal/extract` (`roles.go:38-71, 110-230`, `extract.go:359-372, 410-428`, `version.go` steps), about 80 in `internal/config`, about 500 test lines | none | v0.3 epic 03; no example, scenario, eval case or owner config uses them; `service_version` is unchanged for configurations without them (`omitempty`) |

- STATUS.md "What is NOT verified" lists Bedrock, Vertex and the classifier.
- Removal in 2.0 needs an ADR amending ADR 0004 (providers) and ADR 0009 (loop), and the
  provider docs (`docs/providers/bedrock.md`, `vertex.md`, `classifier.md`; the
  provider docs test requires one page per adapter).
- Binary gain is small (about 0.5 MB); the gain is code, tests, docs and dependency
  surface. The single-group-per-role simplification of `extract.Roles` (no
  `commonCapabilities`/`product`) follows from removing escalation.

## Tasks

- [x] Owner decision L8 (2026-10-19): deprecate escalation, second judge, classifier and `feedback_fallback`; keep Bedrock and Vertex.
- [ ] Design the 2.0 shape of `extract.Roles` (input for the ADR proposal). Skipped by the orchestrator (no CRITIC or ARCH run); the ADR proposal sketches the shape from reading `internal/extract`.
  - Skill: CRITIC, ARCH.
  - Prompt: "Use the architecture-critic agent (or the pasted lens) on internal/extract
    roles and the classifier adapter: what would the loop and
    extract.Roles look like with one group per role and no classifier, how many lines
    and concepts go, and what an operator loses. Then
    `/mattpocock-skills:improve-codebase-architecture` focused on internal/extract/roles.go
    with the same question; write no repository files."
- [x] Deprecate what was decided.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: for `provider: classifier`, `escalation`,
    `second_judge`, `feedback_fallback` and any key only they use,
    keep it working and add one warning from `vde config check` and `vde serve` naming
    the 2.0 removal; mark it deprecated in internal/config/schema.json descriptions,
    docs/operator/configuration.md, the provider pages and CHANGELOG 'Deprecated'.
    No behaviour change. Guardrails: skills.md."
- [x] Draft the 2.0 removal (ADR proposal only).
  - Skill: none (Write).
  - Prompt: "Draft an ADR in docs/architecture/decisions/ with status 'proposed' that
    amends ADR 0004 and ADR 0009 (and revises open-questions decision 14) for the 2.0
    removal of the deprecated items, with the
    line, test and dependency counts above. Do not change code."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`.

## Acceptance criteria

- Deprecated items warn from `vde config check` and `vde serve`; the ADR proposal is
  written.

## Result

- **Warnings.** `config.Validate` warns once per item set, so `vde config check` and
  `vde serve` (both through `loadConfigEnv`) print it and `--strict` fails on it:
  `endpoints.<name>.provider: classifier is deprecated (removal in 2.0); it still works`,
  and `is deprecated (removal in 2.0); it still works` on `roles.extractor.escalation`,
  `roles.judge.escalation`, `roles.second_judge` and `roles.judge.feedback_fallback` (which
  also covers `feedback_group`; `models.<m>.questions` is covered by the classifier
  warning, since it is allowed only there). Nothing else changes: validation, the loop and
  `service_version` are untouched. `TestValidate_deprecated` checks that each item yields
  exactly its warnings and no error (it replaces the two "no issues" cases for escalation
  and the second judge); the CLI test of `tieredConfig` checks the printed warnings.
- **Docs.** `internal/config/schema.json` descriptions, `docs/operator/configuration.md`
  rows, `docs/providers/classifier.md` (a deprecation note), `docs/providers/README.md`,
  README feature table, CHANGELOG "Deprecated".
- **ADR.** [ADR 0011](../../../architecture/decisions/0011-remove-tiers-and-classifier-judge.md),
  status proposed: amends ADR 0004 and ADR 0009, revises decision 14, with counts
  measured now: about 590 production lines (classifier package 171) and about 1,100 test
  lines, no dependency. Listed in the overview. ADR 0004, ADR 0009 and decision 14 are
  unchanged until it is accepted.
- **Deviation.** Production lines go up (+22, the warnings): a deprecation adds the
  warning; the removal in 2.0 takes the lines out.
- Checks: `make check`, `make scenarios` green (no scenario uses the items, so no
  report changes); `make ci` before the commit. Not run: the CRITIC/ARCH step and the Gate
  (orchestrator).

`git diff --stat`: 13 files, +163 −37, plus ADR 0011 (new, 108 lines); production Go +22,
tests +73 −12, the rest docs and roadmap.

## Out of scope

- The removal itself (2.0); Bedrock and Vertex (kept); `blobs.driver: s3` (used by the scaled compose example).

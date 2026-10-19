# Job: Unverified providers and model tiers

Version: v1.1 · Epic: 09 Deprecations · Status: planned · Depends on: owner decision L8 ([open questions](../../open-questions.md#open)); 04-02, 05-01

## Goal

Per the owner's decision, deprecate (in 1.1) the provider adapters and loop extras that
have never run against a real endpoint and that no configuration uses, so that 2.0 can
remove them; or record why they stay.

## Context

| Candidate | Size (prod / test) | Dependencies it alone brings | Notes |
| --- | --- | --- | --- |
| `provider: bedrock` | 241 / 365 | `bedrockruntime`, eventstream | AWS SDK core stays for `blobs.driver: s3` |
| `provider: vertex` | 418 / 450 (about half after 04-02) | `golang.org/x/oauth2`, `cloud.google.com/go/compute/metadata` | |
| `provider: classifier` | 185 / 219 | none | The owner asked for a classifier judge (open question 14, "TypeSafe AI's Jev"); no vendor API was available to test |
| Escalation, `second_judge`, `feedback_fallback: llm` with classifiers | about 180 in `internal/extract` (`roles.go:38-71, 110-230`, `extract.go:359-372, 410-428`, `version.go` steps), about 80 in `internal/config`, about 500 test lines | none | v0.3 epic 03; no example, scenario, eval case or owner config uses them; `service_version` is unchanged for configurations without them (`omitempty`) |

- STATUS.md "What is NOT verified" lists Bedrock, Vertex and the classifier.
- Removal in 2.0 needs an ADR amending ADR 0004 (providers) and ADR 0009 (loop), and the
  provider docs (`docs/providers/bedrock.md`, `vertex.md`, `classifier.md`; the
  provider docs test requires one page per adapter).
- Binary gain is small (about 0.5 MB); the gain is code, tests, docs and dependency
  surface. The single-group-per-role simplification of `extract.Roles` (no
  `commonCapabilities`/`product`) follows from removing escalation.

## Tasks

- [ ] Owner decision L8, per row: deprecate now and remove in 2.0, or keep.
  - Skill: none.
  - Prompt: none.
- [ ] Prepare the decision with a critique.
  - Skill: CRITIC, ARCH.
  - Prompt: "Use the architecture-critic agent (or the pasted lens) on internal/extract
    roles and the bedrock, vertex and classifier adapters: what would the loop and
    extract.Roles look like with one group per role and no classifier, how many lines
    and concepts go, and what an operator loses. Then
    `/mattpocock-skills:improve-codebase-architecture` focused on internal/extract/roles.go
    with the same question; write no repository files."
- [ ] Deprecate what was decided.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: for each deprecated provider value and key,
    keep it working and add one warning from `vde config check` and `vde serve` naming
    the 2.0 removal; mark it deprecated in internal/config/schema.json descriptions,
    docs/operator/configuration.md, the provider pages and CHANGELOG 'Deprecated'.
    No behaviour change. Guardrails: skills.md."
- [ ] Draft the 2.0 removal (ADR proposal only).
  - Skill: none (Write).
  - Prompt: "Draft an ADR in docs/architecture/decisions/ with status 'proposed' that
    amends ADR 0004 and ADR 0009 for the 2.0 removal of the deprecated items, with the
    line, test and dependency counts above. Do not change code."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review medium <first>^..<last>`.

## Acceptance criteria

- Decision recorded per row; deprecated items warn; ADR proposal written if anything is
  deprecated.

## Out of scope

- The removal itself (2.0); `blobs.driver: s3` (used by the scaled compose example).

# Job: One representation selection

Version: v1.1 · Epic: 03 Single source · Status: planned · Depends on: 02-04

## Goal

Submit, worker and CLI select a document's representation through one function, so they
cannot drift. Structure only.

## Context

The sequence "role capabilities, `NewPDFPolicy`, `document.Select`, `GroundingText`" is
written three times: `cmd/vde/extract.go:338-358`, `internal/api/submit.go:670-686`,
`internal/worker/pool.go:381-401`. The worker refuses a job whose selection differs from
the one made at submit ("representation changed since submit"), so the copies must stay
identical. One `extract.SelectRepresentation(ctx, cfg, doc, html, appPDFMode, engine,
policy)` (or a method on `extract.Roles`) replaces them. About −35 lines. Related: the
admin route extracts the error code from `j.Error` inline (`admin.go:483-488`), handled
in 03-02.

## Tasks

- [ ] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: extract one SelectRepresentation function in
    internal/extract from the three copies in cmd/vde/extract.go, internal/api/submit.go
    and internal/worker/pool.go (diff them first and report any difference before
    unifying); replace the three call sites. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Prove equality.
  - Skill: none (make).
  - Prompt: "Run `make check`, `make scenarios`, `make conformance` and `make eval` with
    `--compare` against eval-reports/lean-baseline; report differences only."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- One function selects representations; the three callers call it; all suites green
  with identical eval numbers.

## Out of scope

- Changing selection rules (they are versioned in `service_version`).

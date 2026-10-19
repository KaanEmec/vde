# Job: One representation selection

Version: v1.1 · Epic: 03 Single source · Status: done (Gate task left to the orchestrator) · Depends on: 02-04

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

- [x] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: extract one SelectRepresentation function in
    internal/extract from the three copies in cmd/vde/extract.go, internal/api/submit.go
    and internal/worker/pool.go (diff them first and report any difference before
    unifying); replace the three call sites. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [x] Prove equality.
  - Skill: none (make).
  - Prompt: "Run `make check`, `make scenarios`, `make conformance` and `make eval` with
    `--compare` against eval-reports/lean-baseline; report differences only."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- One function selects representations; the three callers call it; all suites green
  with identical eval numbers.

## Out of scope

- Changing selection rules (they are versioned in `service_version`).

## Result

Diff of the three copies (at 7a5a14e: `cmd/vde/extract.go:342-358`,
`internal/api/submit.go:646-656`, `internal/worker/pool.go:331-351`):

- Capabilities: CLI and submit called `extract.RoleCapabilities(cfg)` per document; the
  worker cached them in `Pool` at `New`. Same input (the validated configuration), same
  result.
- PDF mode: submit `cfg.PDFModeOf(app.ID)`, worker `app.Documents.PDF.Mode` (the same
  configured value), CLI `""`.
- HTML policy: submit and worker resolve the application override with
  `document.ResolveHTMLPolicy`; the CLI takes `--html` or `documents.html`. Kept at the
  callers.
- Grounding text: CLI and worker read it unless the judge policy is `always`; submit never.
  The worker checks "representation changed since submit" between selection and grounding.

`extract.SelectRepresentation(ctx, cfg, doc, html, pdfMode, engine)` does capabilities,
PDF policy, image policy and `document.Select`, and returns the PDF policy for
`document.GroundingText`. Grounding stays with the two callers that need it, so the
worker keeps its order and its log reasons ("representation", "representation changed
since submit", "grounding text"). The worker's cached capabilities are gone.

Checks: `make check`, `make scenarios`, `make conformance` green; `make eval` reports for
`always`, `never` and `auto` identical to `eval-reports/lean-baseline` apart from timings
and `summary.cost_usd`, which left the report in 01-03.

`git diff --stat` (Go, production only, no test changed): 5 files, +30 −33 (net −3). The
−35 estimate assumed the grounding call moved too; it stays at two callers (above).

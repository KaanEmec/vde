# Job: Closeout: changelog, overview, exit criterion check

Version: v0.4 · Epic: 05 Harness, docs and closeout · Status: planned · Depends on: 01, 02

## Goal

v0.4 is recorded as done with its exit criterion checked, or its open items named, and the
architecture documents describe the new packages and data.

## Tasks

- [ ] `CHANGELOG.md`: `## [0.4.0]` with Added (judge policy and override, `extracted`,
      `verification`, `internal/ground`, `documents.pdf`, `internal/pdf`, eval fields,
      `--cases` lists), Changed (ADR 0006 amended, the `config check` PDF warning, the
      `vde extract` exit code 4, the grounding repair under `always` changing
      `service_version`), and the dependency additions with licences.
- [ ] `README.md`: feature list (judge policy, grounding, PDFs without caller pages), binary
      size, memory note.
- [ ] `docs/architecture/overview.md`: status line; packages `internal/ground` and
      `internal/pdf`; the extraction loop pseudocode with the grounding step and the policy
      branches; "Representation selection" rewritten for `documents.pdf`; the data model
      (`jobs.judge_policy`, `jobs.verification`, application `judge` settings); the
      `service_version` inputs; the dependency table; non-goals (server-side rendering
      removed, OCR kept).
- [ ] `docs/roadmap/v0.4-judge-policy-and-preprocessing/README.md`: fill the exit criterion
      check from the owner-suite run (`VDE_USER_DOCS=1` plus a `vde eval --judge auto` run
      on it), the conformance suite, and the binary and image sizes; list what stays open.
- [ ] `docs/roadmap/README.md`: v0.4 marked done (or done with open items).
      `docs/roadmap/v1.0-stable/01-api-freeze`: add `judge`, `verification`, `extracted`,
      `documents.pdf` and the grounding annotations to what the freeze covers.
- [ ] Epic READMEs and job headers updated to `done`, with Outcome sections as in v0.3.

## Acceptance criteria

- `make check` passes. The exit-criterion table has a result in every row, each either
  verified with the test or run named, or listed as open with a reason.

## Out of scope

Tagging and publishing images (release engineering, v1.0 04).

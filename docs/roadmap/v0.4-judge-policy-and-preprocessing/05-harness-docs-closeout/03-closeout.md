# Job: Closeout: changelog, overview, exit criterion check

Version: v0.4 · Epic: 05 Harness, docs and closeout · Status: done · Depends on: 01, 02

## Goal

v0.4 is recorded as done with its exit criterion checked, or its open items named, and the
architecture documents describe the new packages and data.

## Tasks

- [x] `CHANGELOG.md`: `## [0.4.0]` with Added (judge policy and override, `extracted`,
      `verification`, `internal/ground`, `documents.pdf`, `internal/pdf`, eval fields,
      `--cases` lists), Changed (ADR 0006 amended, the `config check` PDF warning, the
      `vde extract` exit code 4, the grounding repair under `always` changing
      `service_version`), and the dependency additions with licences.
- [x] `README.md`: feature list (judge policy, grounding, PDFs without caller pages), binary
      size, memory note.
- [x] `docs/architecture/overview.md`: status line; packages `internal/ground` and
      `internal/pdf`; the extraction loop pseudocode with the grounding step and the policy
      branches; "Representation selection" rewritten for `documents.pdf`; the data model
      (`jobs.judge_policy`, `jobs.verification`, application `judge` settings); the
      `service_version` inputs; the dependency table; non-goals (server-side rendering
      removed, OCR kept).
- [x] `docs/roadmap/v0.4-judge-policy-and-preprocessing/README.md`: fill the exit criterion
      check from the owner-suite run (`VDE_USER_DOCS=1` plus a `vde eval --judge auto` run
      on it), the conformance suite, and the binary and image sizes; list what stays open.
- [x] `docs/roadmap/README.md`: v0.4 marked done (or done with open items).
      `docs/roadmap/v1.0-stable/01-api-freeze`: add `judge`, `verification`, `extracted`,
      `documents.pdf` and the grounding annotations to what the freeze covers.
- [x] Epic READMEs and job headers updated to `done`, with Outcome sections as in v0.3.

## Acceptance criteria

- `make check` passes. The exit-criterion table has a result in every row, each either
  verified with the test or run named, or listed as open with a reason.

## Out of scope

Tagging and publishing images (release engineering, v1.0 04).

## Result (2026-10-18)

Done, with the owner-suite runs pending. `go mod tidy` made go-pdfium and wazero direct
requirements (CI's `go mod tidy -diff` passes again). `CHANGELOG.md` `[0.4.0] - 2026-10-18`
(Added, Changed with the dependencies and licences and the binary sizes, Known gaps).
`README.md`: the three features, status, binary size and memory note.
`docs/architecture/overview.md`: status, the extraction loop with the policy branches,
the repair and the gate, "Representation selection" for `documents.pdf`, the data model
(`jobs.judge_policy`, `jobs.verification`, application judge settings), the
`service_version` inputs and the non-goals. The version README's exit-criterion table
names the tests behind each criterion and marks the owner-suite parts "pending owner run".
`docs/roadmap/README.md` and the v1.0 freeze jobs list the v0.4 supersets and keys. CI runs
`make eval`.

Sizes (`make cross`, `CGO_ENABLED=0 -trimpath -s -w`): linux/amd64 35,979,424 bytes and
linux/arm64 34,209,952 bytes, against 26,812,576 and 25,165,984 for v0.3.0 built the same
way; darwin/arm64 34.9 MB. Both linux binaries are static.

Deviations:

- The container image was not built: the build machine's Docker has no buildx, which the
  Dockerfile's `--platform=$BUILDPLATFORM` needs. The Dockerfile comment and the changelog
  give the binary sizes and the distroless base (about 2 MB).
- The owner-suite check (`VDE_USER_DOCS=1` plus `vde eval --judge auto` with real models)
  was not run here; the commands are in `docs/quality.md`.

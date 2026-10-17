# Job: Operator and API documentation

Version: v0.4 · Epic: 05 Harness, docs and closeout · Status: done · Depends on: 01-03, 03-03, 04-03

## Goal

An operator can choose a judge policy, annotate a schema for grounding and configure PDF
preprocessing from the documentation alone. A caller can read `extracted`, `verification`
and `grounding` entries correctly.

## Tasks

- [x] `docs/operator/judge-policy.md` (new): what `always`, `never` and `auto` do; how to
      choose (contract fidelity and high stakes: `always`; cheap bulk extraction with
      review downstream: `never`; text-heavy documents with a cost target: `auto`); the
      sample rate and the disagreement metric; what grounding can and cannot catch (from
      ADR 0009); the `x-vde-grounding` annotations with a worked schema; the normalization
      rules and the date table (generated from `internal/ground` or checked against it by a
      test); `allow_override` and the `judge` request part.
- [x] `docs/operator/configuration.md`: rows for `roles.judge.policy`,
      `applications[].judge.*`, `documents.pdf.*` and `applications[].documents.pdf.mode`,
      with a paragraph on the memory budget, the compilation cache directory, fonts and the
      interpreter fallback.
- [x] `docs/operator/README.md` and `docs/operator/troubleshooting.md`: link the new page,
      and add entries for "PDF refused: too many pages", "grounded share is low" (missing
      annotations on derived fields) and "pdfium cache not writable".
- [x] `docs/api/guide.md`: the `judge` part, `400` when not allowed, the status table row
      for `extracted`, `verification`, `source: grounding`, and "Interpreting the status"
      updated: `accepted` means the judge accepted, or under an opted-in `auto` that the
      grounding check passed. `verification` says which.
- [x] `docs/api/writing-instructions.md`: a short section on schema annotations for
      grounding.
- [x] `docs/conformance/travelinbox-v1-review.md`: finish the section 2 rows and the
      section 6 open point from 01-03, and note the grounding repair under `always`
      (visible only through `service_version`).
- [x] `docs/quality.md`: section "Judge policy" with the measurement plan (owner suite and
      the TravelInbox corpus, the same models under `always`, `never` and `auto`; columns:
      false accepts by verification, judge calls, tokens, cost per correct case) and the
      line "Not run yet: the owner runs this comparison."
- [x] `docs/roadmap/backlog.md`: mark "Server-side PDF page rendering" as resolved by
      ADR 0010.
- [x] `docs/architecture/decisions/0006-representation-policy.md`: add a line under its
      status, "Amended by ADR 0010 (PDF preprocessing in the service)".

## Acceptance criteria

- A docs test (like `internal/provider/docs_test.go`) checks that every configuration key
  in `internal/config` appears in `docs/operator/configuration.md`, and that the date
  forms listed in `judge-policy.md` match `ground` (the date table).
- Every link in the changed files resolves (the existing link check, or a one-off run
  recorded in the PR).

## Out of scope

The documentation site (v1.0 04-02).

## Result (2026-10-18)

Done. New `docs/operator/judge-policy.md` (policies and the gate, how to choose,
configuration and override, what grounding catches and misses, `x-vde-grounding` with a
worked invoice schema and a feedback example, normalization, the date and time table,
metrics, `extracted`). Updated: `configuration.md` (PDF memory budget, compilation cache,
fonts and interpreter fallback; links; the Ollama example note), operator `README.md`,
`troubleshooting.md` ("PDF refused: too many pages", "Grounded share is low", "pdfium
cache not writable"), `evaluation.md` (`--judge`, case lists, the new numbers, comparing
policies), `pdf.md` (sizes, links), `docs/api/guide.md` (`accepted` by judge or grounding,
`extracted` row, `verification` in the example, `source: grounding`, the policy table, the
`judge` part's `400`s), `docs/api/writing-instructions.md` ("Annotating the schema for
grounding"), `docs/conformance/travelinbox-v1-review.md` (section 2 rows as implemented,
the PDF row rewritten, the grounding repair under `always`, the two section 6 open
points), `docs/security.md` (pdfium sandbox control), `docs/quality.md` ("Judge policy"
plan with the owner's commands, "Not run yet"), `docs/roadmap/backlog.md` (server-side
rendering resolved), ADR 0006 ("Amended by ADR 0010"), `api/openapi.yaml` (`result` and
`feedback` descriptions for `extracted`), `examples/README.md` and every example
configuration (commented `roles.judge.policy` and `documents.pdf` blocks).

Tests: `TestEveryKeyIsDocumented` (`internal/config/docs_test.go`: every YAML key of
`config.Config` in a code span of `configuration.md`) and `TestDateTableIsDocumented`
(`internal/ground/docs_test.go`: every date and time form, month language and the rules
version in `judge-policy.md`, and no form the code lacks). Links in the changed files were
checked with a one-off script.

Deviations:

- The docs tests are test-only files in `internal/config` and `internal/ground`; neither
  package's behaviour changed.
- `TestEveryKeyIsDocumented` found `roles.extractor.policy` written as "`policy` on
  `roles.extractor`"; the row now names the key.

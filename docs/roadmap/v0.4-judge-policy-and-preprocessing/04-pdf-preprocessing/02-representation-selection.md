# Job: `documents.pdf` and representation selection

Version: v0.4 · Epic: 04 PDF preprocessing · Status: planned · Depends on: 01

## Goal

When a role's groups cannot read `application/pdf` natively, the service chooses text or
rendered pages per `documents.pdf.mode`, instead of refusing a PDF that arrives without
pages.

## Context

ADR 0010. The decision is made at submit, from the page count and the text layer. The
worker builds the parts, and rendered pages are not stored. The worker already re-selects
the representation and fails a job whose representation changed
(`TestRepresentationChangedFails`), so the selection must be deterministic for the same
bytes and configuration. Caller pages win over rendering. Both roles get the same
representation.

## Tasks

- [ ] `internal/config`: `documents.pdf` with `mode` (`auto|native|text|pages`, default
      `auto`), `dpi` (150, range 72 to 300), `max_pages` (20, 1 to 200),
      `min_text_chars_per_page` (200, ≥ 0) and `workers` (1, 1 to 16). Validation and
      defaults are in `validate.go`. Per application: `applications[].documents.pdf.mode`
      only, the others are process-wide.
- [ ] `internal/extract/version.go`: hash mode, dpi and min chars (not `workers`) into
      `service_version`, only when the mode is not `native` or a group cannot read PDF, so
      v0.3 hashes stay for PDF-capable deployments.
- [ ] `internal/document/representation.go`: `SelectWith` gains a `PDFPolicy` (mode, dpi,
      min chars, the engine). Order for a PDF: native when both roles read it; caller pages
      when given and readable; then by mode. `auto`: text when every page has at least
      `min_text_chars_per_page` characters after trimming whitespace, else rendered pages.
      `text` and `pages` force one. `native` refuses as today. A rendered-pages candidate
      is offered only when both roles read `image/png` (or the converted type).
- [ ] `internal/document`: `Representation.Kind` gains no new value. Text from a PDF is
      `Text`, rendered pages are `Pages`. A new field `Source` (`given`, `text_layer`,
      `rendered`) is stored in the `representation` metadata column as `pages+rendered` or
      `text+text_layer`, and shown in admin job metadata.
- [ ] Submit (`internal/api/submit.go`, `submit_json.go`, `sync.go`): open the PDF, count
      pages and measure text under the engine's timeout. Too many pages answers
      `415 unsupported_document` with a message naming the limit. An encrypted or invalid
      PDF answers `415` with a generic message.
- [ ] Worker (`internal/worker/pool.go`): build `Pages` parts by rendering at the configured
      dpi, then apply the image policy (`documents.images.max_dimension`). Text parts come
      from the text layer, pages joined with a form-feed separator and a `--- page N ---`
      marker.
- [ ] `cmd/vde/extract.go` and `cmd/vde/eval.go`: the same selection, with an engine
      created per process.
- [ ] `cmd/vde/serve.go`: one `pdf.Engine` per process, closed on shutdown. With
      `--role api` it is used for selection only. Rendering happens on workers.
- [ ] `internal/config/validate.go` and `cmd/vde/config.go`: replace the warning "no
      endpoint-model pair in group … reads application/pdf natively" with: under `native`,
      the old warning; otherwise an info line "PDFs are sent as text or rendered pages
      (documents.pdf.mode: auto)" with the memory estimate (`workers` × about 500 MB), and a
      warning when the interpreter fallback is active.

## Acceptance criteria

- `representation_test.go`: a text-rich PDF under `auto` with text-only and image-only
  groups gives `Text` and `Pages` respectively. A PDF with one blank page under `auto`
  gives `Pages`. `text` with too little text still gives `Text`. `native` without pages
  gives `unsupported_document`. Caller pages win over rendering. A PDF-capable pair still
  gets `Native`.
- Determinism: selecting twice on the same bytes gives the same `Kind`, `Source` and parts.
- `internal/api/submit_test.go`: a PDF over `max_pages` without native readers answers
  `415`. A PDF with a PDF-capable group is unaffected.
- `internal/worker/pool_test.go`: a rendered-pages job reaches the fake provider with
  `image/png` parts at the expected size. A resumed job renders identical bytes.
- Version tests: a PDF-capable configuration keeps its v0.3 hash.

## Out of scope

Mixed representations (text plus images in one request), per-page mode decisions, storing
rendered pages.

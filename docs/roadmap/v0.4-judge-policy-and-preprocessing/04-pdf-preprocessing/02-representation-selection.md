# Job: `documents.pdf` and representation selection

Version: v0.4 · Epic: 04 PDF preprocessing · Status: done · Depends on: 01

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

- [x] `internal/config`: `documents.pdf` with `mode` (`auto|native|text|pages`, default
      `auto`), `dpi` (150, range 72 to 300), `max_pages` (20, 1 to 200),
      `min_text_chars_per_page` (200, ≥ 0) and `workers` (1, 1 to 16). Validation and
      defaults are in `validate.go`. Per application: `applications[].documents.pdf.mode`
      only, the others are process-wide.
- [x] `internal/extract/version.go`: hash mode, dpi and min chars (not `workers`) into
      `service_version`, only when the mode is not `native` or a group cannot read PDF, so
      v0.3 hashes stay for PDF-capable deployments.
- [x] `internal/document/representation.go`: `SelectWith` gains a `PDFPolicy` (mode, dpi,
      min chars, the engine). Order for a PDF: native when both roles read it; caller pages
      when given and readable; then by mode. `auto`: text when every page has at least
      `min_text_chars_per_page` characters after trimming whitespace, else rendered pages.
      `text` and `pages` force one. `native` refuses as today. A rendered-pages candidate
      is offered only when both roles read `image/png` (or the converted type).
- [x] `internal/document`: `Representation.Kind` gains no new value. Text from a PDF is
      `Text`, rendered pages are `Pages`. A new field `Source` (`given`, `text_layer`,
      `rendered`) is stored in the `representation` metadata column as `pages+rendered` or
      `text+text_layer`, and shown in admin job metadata.
- [x] Submit (`internal/api/submit.go`, `submit_json.go`, `sync.go`): open the PDF, count
      pages and measure text under the engine's timeout. Too many pages answers
      `415 unsupported_document` with a message naming the limit. An encrypted or invalid
      PDF answers `415` with a generic message.
- [x] Worker (`internal/worker/pool.go`): build `Pages` parts by rendering at the configured
      dpi, then apply the image policy (`documents.images.max_dimension`). Text parts come
      from the text layer, pages joined with a form-feed separator and a `--- page N ---`
      marker.
- [x] `cmd/vde/extract.go` and `cmd/vde/eval.go`: the same selection, with an engine
      created per process.
- [x] `cmd/vde/serve.go`: one `pdf.Engine` per process, closed on shutdown. With
      `--role api` it is used for selection only. Rendering happens on workers.
- [x] `internal/config/validate.go` and `cmd/vde/config.go`: replace the warning "no
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

## Progress

Done (wave 1): configuration and validation, the selection in `internal/document`, `vde
extract`, and `config check`. Done with epic 03's wiring (job 03-03, 2026-10-17): the PDF
policy in `service_version`, submit (415 naming `documents.pdf.max_pages`), the worker
(re-selection with the same policies and engine, `TestPDFPreparedByService` for text layer
and rendered pages with a stub engine), `vde eval` and `vde serve` (one engine per
process for submit and the workers, also with `--role api`). Originally open, in files
owned by the judge-policy wiring of this wave:
hashing the PDF policy into `service_version` (`internal/extract/version.go`, use
`document.PDFPolicy.String()`), submit (`internal/api`), the worker (`internal/worker`),
`vde eval` and `vde serve`. Until then the service keeps the v0.3 behaviour, since its
callers pass no PDF policy.

Wiring for each of them: build one engine per process with
`pdf.New(pdf.Config{Workers: cfg.Documents.PDF.Workers, CacheDir: pdf.CacheDir(cfg.Server.DataDir),
MaxPages: cfg.Documents.PDF.MaxPages, MaxBytes: cfg.Limits.DocumentBytes})` (`newPDFEngine` in
`cmd/vde/pdf.go`), close it on shutdown, log `Engine.CacheErr()` as a warning, and pass
`document.NewPDFPolicy(cfg.Documents.PDF, app.Documents.PDF.Mode, document.PDFEngineOf(engine))`
as the last argument of `SelectWith`, at submit and in the worker alike (the worker must
re-select with the same policy, or a PDF chosen as text at submit fails as unsupported).
Store `Representation.Label()` in the `representation` column.

## Deviations

- `SelectWith` takes the PDF policy as an optional trailing argument
  (`pdfs ...PDFPolicy`), so the callers in other packages keep compiling unchanged; only
  the first is used. A nil engine or the `native` mode keeps the v0.3 selection.
- The PDF engine is the `document.PDFEngine` interface (`Open` returning a `PDFDoc`), with
  `PDFEngineOf(*pdf.Engine)` as the adapter, so tests stub it.
- Selection renders the pages itself (it builds the parts), instead of deciding at submit
  and rendering in the worker: one code path, deterministic for the same bytes and policy
  (two renders are byte-identical). Splitting decision and rendering is left to the
  worker wiring if submit latency matters (a 20-page PDF renders in about 0.4 s).
- `Source` has the values `given`, `text_layer` and `rendered`; `Representation.Label()`
  gives `pages+rendered`, `text+text_layer`, or the kind alone for `given`.
- `PDFPolicy.MaxPages` is checked after opening (any PDF the service prepares), and the
  engine refuses above `documents.pdf.max_pages` too. The refusal names the limit.
- Validation: a group without a native PDF reader is a missing type only when it cannot
  read the PDF under the mode (images for `pages`, text for `text`, either for `auto`). A
  text-only group under `auto` gets the warning "reads PDFs only through their text
  layer". `config check` prints a `pdf:` line per role and the `documents.pdf` line with
  the memory estimate (`workers` × 500 MB), and warns when wazero would interpret.
  Validation itself has no info level, so the info line is in the summary.
- The CLI's compilation cache is `os.UserCacheDir()/vde/pdfium-cache`.

## Out of scope

Mixed representations (text plus images in one request), per-page mode decisions, storing
rendered pages.

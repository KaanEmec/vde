# ADR 0010: PDF preprocessing in the service (text layer and page rendering)

Status: accepted, 2026-10-17 · Amends: [ADR 0006](0006-representation-policy.md)

## Context

ADR 0006 decided that "the service never renders PDF pages": operators configure a
PDF-capable model, or callers send page images. Three things changed:

- Cheap models that read well but do not take PDFs natively (GLM, DeepSeek) did best on cost
  in the owner's runs of 2026-10-17. To test them, the owner rasterized PDFs by hand with
  `pdftoppm` and sent the images as `page` parts, which a real caller should not have to do.
- The grounding check of [ADR 0009](0009-judge-policy-and-grounding.md) needs document
  text. For a PDF, that text is its text layer, which the service cannot read today.
- Most booking and invoice PDFs are text-rich. Sent as text, they cost a fraction of page
  images and can be grounded.

The reason ADR 0006 gave against rendering was the build: a rasterizer meant cgo, a larger
image and a native dependency. A spike on 2026-10-17 showed that this no longer holds.

## Decision

- **The service reads and renders PDFs itself** with `github.com/klippa-app/go-pdfium`
  (v1.21.3) on its WebAssembly runtime (`github.com/tetratelabs/wazero` v1.12). The pdfium
  WebAssembly module is embedded with `go:embed`. The build stays `CGO_ENABLED=0`, a single
  static binary, and cross-compiles to linux/amd64 and linux/arm64.
- **New package `internal/pdf`**: `Open(ctx, data) (*Doc, error)`, `Doc.Pages()`,
  `Doc.Text(page)`, `Doc.Render(page, dpi) ([]byte, error)` (PNG, encoded with `image/png`)
  and `Doc.Close()`. It runs on a bounded pool of pdfium instances (`documents.pdf.workers`,
  default 1), each call under a timeout, with page and size limits. The pool starts lazily
  on the first PDF that needs it, and compiled code is cached under
  `server.data_dir/pdfium-cache`.
- **Configuration `documents.pdf`**: `mode: auto | native | text | pages` (default `auto`),
  `dpi` (150), `max_pages` (20), `min_text_chars_per_page` (200), `workers` (1). When a
  role's groups cannot read `application/pdf` natively:
  - `auto` sends the text layer as text when every page has at least
    `min_text_chars_per_page` characters, and renders pages otherwise;
  - `text` and `pages` force one of the two;
  - `native` is ADR 0006's behaviour: without caller pages, the job is refused with
    `415 unsupported_document`.
  When a group reads PDFs natively, the PDF is still sent as given, as before. Caller-supplied
  `page` parts still win over rendering. Both roles always get the same representation.
- **The text layer is kept for grounding** even when the models get page images or the PDF
  itself. It is never sent to a model unless the representation is text, and never logged.
- **Where the work happens.** At submit, the service opens the PDF, counts its pages and
  measures the text layer to choose the representation and refuse early. The worker renders
  the pages when it builds the parts. Rendered pages are not stored as blobs: a resumed job
  renders them again from the same bytes, with the same result.
- **Unchanged from ADR 0006**: raw HTML by default, image conversion as it is, HEIC not
  converted, no OCR.

## Size and performance budget (spike, 2026-10-17)

| Item | Measured |
| --- | --- |
| Embedded wasm module | 5.7 MB |
| Binary growth | about 15 MB |
| Cold start of an instance (wasm compile) | about 1.1 s; about 35 ms with the compilation cache (about 20 MB on disk) |
| Text layer, per page | 1.5 to 6 ms |
| Render at 150 dpi, A4 page | 2 to 40 ms |
| PNG encode, per page | 12 to 40 ms |
| Peak RSS per instance | 230 to 490 MB |
| Dependency graph | 56 lines in `go mod graph`; notable additions are wazero, go-commons-pool and google/uuid |

The budget we accept: a 20-page PDF prepared in under 2 s on one core with a warm cache, at
most about 500 MB of memory per configured worker, and a binary under its v0.3 size plus
20 MB. With its memory per instance, `workers` defaults to 1 and is documented as a memory
decision. wazero's compiler exists on amd64 and arm64. On other architectures the
interpreter is about 60 times slower, and `config check` says so.

## Consequences

- `go-pdfium` (MIT), `wazero` (Apache-2.0) and pdfium itself (BSD-3-Clause) join the
  allowed dependencies. A `NOTICE` file lists them.
- The image grows by about 15 MB, and a process that handles PDFs needs memory for its
  pdfium instances. Operators who never send PDFs to non-PDF models pay nothing at run time
  because of the lazy start.
- The sandbox has no filesystem and no system fonts. A PDF that uses non-embedded,
  non-standard fonts may render with fallback glyphs. This is a known limitation, and the
  text layer is unaffected.
- `config check` no longer warns that a deployment "depends on callers sending pages" when a
  group cannot read PDFs. It says which `documents.pdf.mode` applies, and with `native` it
  keeps the old warning.
- The PDF policy (mode, dpi, minimum characters) is hashed into `service_version`.
- The hand-rasterized copies used in the owner's test runs become obsolete.
- The backlog entry "Server-side PDF page rendering" is resolved by this ADR.

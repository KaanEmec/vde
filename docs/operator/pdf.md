# PDF preprocessing

Since v0.4 the service reads a PDF's text layer and renders its pages itself, so models
that do not accept `application/pdf` can still process PDFs without the caller sending
page images ([ADR 0010](../architecture/decisions/0010-pdf-preprocessing-in-service.md)).
It uses pdfium compiled to WebAssembly (go-pdfium on wazero): the binary stays a single
static, cgo-free file.

## Which representation a PDF gets

At submit the service chooses one representation, the same for the extractor and the
judge, in this order:

1. **As given**, when a pair of every role's groups accepts `application/pdf`. Nothing is
   rendered.
2. **Caller pages**, when the request has `page` parts that the groups read.
3. Otherwise by `documents.pdf.mode`:

| Mode | What the models get |
| --- | --- |
| `auto` (default) | The text layer as text when every page has at least `min_text_chars_per_page` characters (200); otherwise the pages rendered at `dpi` (150). A text-rich PDF falls back to rendered pages when a group reads only images. |
| `text` | Always the text layer, even if it is thin. |
| `pages` | Always rendered pages. |
| `native` | Nothing is prepared (the v0.3 behaviour): without caller pages the job is refused with `415 unsupported_document`. |

Text is sent as one part, each page under a `--- page N ---` marker, pages separated by a
form feed. Rendered pages are PNG, drawn on white without interactive form fields, and
then pass through `documents.images` like any page image (downscaled and converted to
JPEG when larger than `max_dimension`). A PDF with more than `max_pages` pages (20), an
encrypted PDF and an unreadable one are refused with `415 unsupported_document`, unless
they are sent as given to a PDF-capable group. Applications can override the mode with
`applications[].documents.pdf.mode`.

The text layer is also what the grounding check reads ([ADR 0009](../architecture/decisions/0009-judge-policy-and-grounding.md), [judge policy](judge-policy.md)), whatever the models
received, when its pages average at least `min_text_chars_per_page` characters. It is
never sent to a model unless the representation is text, and never logged. When the
caller sends pages, grounding still reads the PDF's own text layer, not the pages.

`vde config check` prints, per role, how its groups get a PDF, and the memory estimate.

## Memory and CPU

Each pdfium instance takes about 230 to 490 MB while it works (the WebAssembly memory is
capped at 1 GiB). `documents.pdf.workers` (1) bounds the instances per process, so budget
about 500 MB per worker on top of the service. A document holds an instance while it is
prepared; other PDFs wait for it (at most 30 s per call, after which the instance is
discarded and replaced). Measured on an Apple M-series laptop (warm cache):

| Step | Time |
| --- | --- |
| First start of pdfium in a process, compilation cache warm | about 35 ms |
| First start, cache cold (compiles the module) | about 1.2 s |
| Text layer of a page | 0.5 to 3 ms |
| Render at 150 dpi and PNG encode, A4 page | about 16 to 20 ms |
| 20-page text-rich PDF: text, then all pages rendered | 33 ms, 0.41 s |

pdfium starts on the first PDF that needs it: a deployment whose models all read PDFs
never starts it. On amd64 and arm64 wazero compiles the module to native code; on other
architectures it interprets it, about 60 times slower, and `config check` warns.

## Compilation cache

The compiled module is cached under `server.data_dir/pdfium-cache` (about 20 MB, created
with mode `0700`); CLI commands use the user cache directory (`vde/pdfium-cache` under
`os.UserCacheDir`). Without a writable directory the service runs uncached and compiles at
every start. The cache can be deleted at any time.

## Limitations

- **Fonts.** The sandbox has no filesystem and no system fonts. A PDF that uses fonts it
  does not embed (other than the 14 standard PDF fonts) may render with fallback glyphs.
  The text layer is unaffected.
- **No OCR.** A scanned PDF has no text layer: under `auto` it is rendered, and it is not
  groundable.
- **Binary size.** pdfium adds about 9 MB to the binary (5.7 MB of WebAssembly): v0.4 is
  35.9 MB on linux/amd64 and 34.2 MB on linux/arm64, against 26.8 and 25.2 MB in v0.3. Its
  licenses (BSD-3-Clause for pdfium, MIT for go-pdfium, Apache-2.0 for wazero) are listed
  in `NOTICE`.

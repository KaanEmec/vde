# Epic 04: PDF preprocessing (text layer and rendering)

Version: v0.4 · Status: done · Depends on: v0.3 (independent of 01 to 03)

The service reads a PDF's text layer and renders its pages itself, cgo-free, with
`go-pdfium` on its WebAssembly runtime. Models without native PDF input then work without
caller-supplied pages, text-rich PDFs go to them as cheap text, and every PDF with a text
layer can be grounded. See [ADR 0010](../../../architecture/decisions/0010-pdf-preprocessing-in-service.md)
(amends ADR 0006).

The feasibility spike is done (2026-10-17). go-pdfium v1.21.3 with wazero v1.12 builds with
`CGO_ENABLED=0` to a static binary for linux/amd64 and linux/arm64. The 5.7 MB wasm module
is embedded and the binary grows by about 15 MB. Cold start takes about 1.1 s, or about
35 ms with the compilation cache. A page's text takes 1.5 to 6 ms, a render at 150 dpi 2 to
40 ms, and a PNG encode 12 to 40 ms. Peak RSS is 230 to 490 MB per instance. Measurements
and budget are in ADR 0010.

| # | Job | Status |
| --- | --- | --- |
| 01 | [`internal/pdf` on go-pdfium (WebAssembly)](01-pdf-package.md) | done (image sizes, `FuzzOpen` and cross builds in CI at closeout) |
| 02 | [`documents.pdf` and representation selection](02-representation-selection.md) | done |
| 03 | [Text layer for grounding, samples and the owner suite](03-text-layer-and-samples.md) | done |

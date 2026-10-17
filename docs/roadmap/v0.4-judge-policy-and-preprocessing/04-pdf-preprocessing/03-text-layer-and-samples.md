# Job: Text layer for grounding, samples and the owner suite

Version: v0.4 · Epic: 04 PDF preprocessing · Status: done · Depends on: 02, 02-03

## Goal

Every PDF with a usable text layer is groundable, whatever the models receive. The owner
suite's PDFs run from their bytes alone.

## Context

ADR 0009 needs document text for the `auto` gate. ADR 0010 keeps the text layer beside page
images and native PDFs for grounding. The hand-rasterized copies made with `pdftoppm` for
the owner's runs on 2026-10-17 become obsolete.

## Tasks

- [x] `internal/document/representation.go`: for every PDF representation (`Native`,
      `Pages` from the caller or rendered, `Text`), set `GroundText` from the text layer
      when it has at least `min_text_chars_per_page` characters on average. Otherwise the
      representation is not groundable. When the selection already opened the PDF (text or
      rendered pages), the text layer comes at no extra cost. A PDF sent natively is opened
      for grounding only under `auto` and `never`. Under `always` it is not opened, so a
      deployment with PDF-capable models never starts pdfium (the lazy start promised in
      ADR 0010).
- [x] A text layer that disagrees with caller-supplied pages (different documents) is not
      detectable. Document that grounding uses the PDF's own text, not the pages.
- [x] `internal/document/userdocs_test.go` (`VDE_USER_DOCS=1`): per PDF, report the page
      count, characters per page, the chosen representation under `auto` for a text-only
      and an image-only group, and the timings. Metadata only.
- [x] Note in `docs/operator/evaluation.md` (a new "Owner suite" paragraph) that the
      hand-rasterized copies in the untracked `testdata/user-docs` are obsolete and can be
      deleted, since runs use the original PDFs.
- [x] `docs/operator/configuration.md`: `documents.pdf.*` rows, the memory budget per worker,
      the compilation cache, the fonts limitation, and the interpreter fallback (full
      wording in epic 05).

## Acceptance criteria

- `examples/samples/booking.pdf` under each mode is groundable, and a correct candidate for
  it yields no grounding entries.
- A generated image-only PDF (a page with an embedded image and no text) is not groundable.
  Under `auto` with a text-only group it is refused, and with an image-capable group it is
  rendered.
- An end-to-end test in `cmd/vde/extract_test.go`: `vde extract --judge auto` on
  `booking.pdf` with a configuration whose pairs declare no `application/pdf` runs without
  `--page` flags and ends `accepted` with `verification: grounded` on a fake grounded
  candidate.
- With `VDE_USER_DOCS=1`, every PDF of the owner suite is selected without caller pages.
  The run is recorded in the epic 05 exit-criterion check.

## Progress

Done (wave 1): grounding text for every PDF representation, `GroundingText` for PDFs sent
natively or as caller pages, the owner-suite report and the configuration rows (plus
`docs/operator/pdf.md`). Open: the "Owner suite" paragraph in `docs/operator/evaluation.md`,
the grounding acceptance checks that need `internal/ground` and the loop (a correct
candidate for `booking.pdf` yields no entries; `vde extract --judge auto` ends `accepted`
with `verification: grounded`), which belong to epic 03's wiring. Both were done there
(job 03-03): `TestExtractPDFWithoutPages` runs `booking.pdf` as text layer, rendered pages
and native, under `always` and `auto`; under `auto` each ends `accepted`/`grounded` with
one provider call (so no critical grounding miss), and a native PDF is grounded only under
`auto`. `TestServeJudgePolicies` does the same over HTTP.

Owner suite, `VDE_USER_DOCS=1` (2026-10-17): all 5 PDFs are selected without caller
pages, as `text+text_layer` for a text-only group and `pages+rendered` for an image-only
group, all groundable. Pages per PDF 1 to 5, characters per page 238 to 13 857; text
layer 10 to 45 ms per PDF (322 ms for the first, which includes the engine start),
selection with rendering 0.11 to 0.55 s.

## Deviations

- The field is `GroundText` (as in job 02-03) with `Groundable`. Selection fills it when
  it opened the PDF (text or rendered pages). For a PDF sent natively or as caller pages
  selection does not open it; `document.GroundingText(ctx, doc, rep, pdfPolicy)` does,
  and the loop calls it only when the judge policy needs grounding (`auto`, `never`), so
  `always` never starts pdfium.
- Grounding text from a PDF is the trimmed pages joined by blank lines, without the
  `--- page N ---` markers the model text has (a marker's page number would ground small
  numbers). Groundable means at least `min_text_chars_per_page` characters per page on
  average, and at least one.
- `vde extract` on `booking.pdf` with pairs that declare no `application/pdf` is tested
  without `--page` (`TestExtractPDFWithoutPages`, text-only and image-only groups, fake
  provider, ends accepted with groundable text); the `--judge auto` and `grounded` part
  waits for the loop.
- The image-only PDF test also checks the white page background.

## Out of scope

OCR for image-only PDFs, cross-checking pages against the text layer.

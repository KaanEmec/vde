# Job: Text layer for grounding, samples and the owner suite

Version: v0.4 · Epic: 04 PDF preprocessing · Status: in progress · Depends on: 02, 02-03

## Goal

Every PDF with a usable text layer is groundable, whatever the models receive. The owner
suite's PDFs run from their bytes alone.

## Context

ADR 0009 needs document text for the `auto` gate. ADR 0010 keeps the text layer beside page
images and native PDFs for grounding. The hand-rasterized copies made with `pdftoppm` for
the owner's runs on 2026-10-17 become obsolete.

## Tasks

- [ ] `internal/document/representation.go`: for every PDF representation (`Native`,
      `Pages` from the caller or rendered, `Text`), set `GroundText` from the text layer
      when it has at least `min_text_chars_per_page` characters on average. Otherwise the
      representation is not groundable. When the selection already opened the PDF (text or
      rendered pages), the text layer comes at no extra cost. A PDF sent natively is opened
      for grounding only under `auto` and `never`. Under `always` it is not opened, so a
      deployment with PDF-capable models never starts pdfium (the lazy start promised in
      ADR 0010).
- [ ] A text layer that disagrees with caller-supplied pages (different documents) is not
      detectable. Document that grounding uses the PDF's own text, not the pages.
- [ ] `internal/document/userdocs_test.go` (`VDE_USER_DOCS=1`): per PDF, report the page
      count, characters per page, the chosen representation under `auto` for a text-only
      and an image-only group, and the timings. Metadata only.
- [ ] Note in `docs/operator/evaluation.md` (a new "Owner suite" paragraph) that the
      hand-rasterized copies in the untracked `testdata/user-docs` are obsolete and can be
      deleted, since runs use the original PDFs.
- [ ] `docs/operator/configuration.md`: `documents.pdf.*` rows, the memory budget per worker,
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

## Out of scope

OCR for image-only PDFs, cross-checking pages against the text layer.

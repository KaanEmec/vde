# Job: Document text for grounding

Version: v0.4 · Epic: 02 Grounding check · Status: done · Depends on: 02

## Goal

Every representation carries the text the grounding check runs against, or says that there
is none. The text is computed once, never sent to a model unless the representation is
text, and never logged.

## Context

What the model reads and what grounding reads can differ. For raw HTML the model gets the
source, while grounding needs the visible text. For a PDF read natively or as page images,
grounding needs the text layer (epic 04). Images have no text, since there is no OCR
(backlog).

## Tasks

- [x] `internal/document/representation.go`: `Representation.GroundText string` and
      `Groundable bool`. Text, markdown and email: the text given to the model (email: the
      meaningful headers and the body). HTML: the visible text (`html.go`'s converter, link
      targets included) whatever the HTML policy. PDF: empty and not groundable until epic
      04 job 03 fills it from the text layer. Images and caller pages: not groundable.
- [x] `internal/ground`: `NewText(s string) *Text` builds the normalized index once per
      job. `internal/extract` builds it lazily, only when a policy or repair needs it.
- [x] `internal/logging/redaction_test.go`: add `GroundText` to the content that must never
      appear in logs.
- [x] `internal/document/userdocs_test.go`: with `VDE_USER_DOCS=1`, report per file whether
      it is groundable and the text length (metadata only).

## Acceptance criteria

- `representation_test.go`: groundable text for `.txt`, `.md`, `.html` under both policies
  (visible text in both), and `.eml`. Not groundable for an image, for a PDF until 04-03, or
  for pages.
- A grounding test over `examples/samples/booking.html` and `booking.eml` with the sample
  schema and a correct candidate yields no entries.
- The redaction test passes with `GroundText` in its list.

## Deviations

- `Representation.GroundText` and `Groundable` are set by selection: plain text and
  markdown as given; HTML as visible text (link targets included) under both policies;
  email as the header block and the body, an HTML body as visible text. Images, caller
  pages and HTML or email without text are not groundable.
- PDFs did not wait for epic 04: the same wave filled their text layer (04-03). A PDF is
  groundable when selection read it as text or rendered pages, or through
  `document.GroundingText(ctx, doc, rep, pdfPolicy)` when it is sent natively or as caller
  pages. `GroundingText` returns `rep.GroundText` for every other document, so the loop
  can call it for any representation.
- `NewText` was built in job 01 (`internal/ground`). Building it lazily in
  `internal/extract` (once per run, on the first schema-valid candidate of a groundable
  representation) and the redaction test (the planted document sentinel is the
  `GroundText` of a text document; new cases run the repair, `never` and `auto`) were done
  with epic 03's wiring (job 03-03).
- The grounding test over `booking.html` and `booking.eml` with a correct candidate needs
  `ground.Check` from the same wave: `TestGroundingText` checks that the text holds the
  sample's values (`HV-48213`, `EUR 412.50`) and no markup; the end-to-end check is epic
  03's.

## Out of scope

The PDF text layer (epic 04), OCR.

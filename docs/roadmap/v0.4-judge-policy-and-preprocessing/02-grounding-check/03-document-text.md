# Job: Document text for grounding

Version: v0.4 · Epic: 02 Grounding check · Status: in progress · Depends on: 02

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

- [ ] `internal/document/representation.go`: `Representation.GroundText string` and
      `Groundable bool`. Text, markdown and email: the text given to the model (email: the
      meaningful headers and the body). HTML: the visible text (`html.go`'s converter, link
      targets included) whatever the HTML policy. PDF: empty and not groundable until epic
      04 job 03 fills it from the text layer. Images and caller pages: not groundable.
- [ ] `internal/ground`: `NewText(s string) *Text` builds the normalized index once per
      job. `internal/extract` builds it lazily, only when a policy or repair needs it.
- [ ] `internal/logging/redaction_test.go`: add `GroundText` to the content that must never
      appear in logs.
- [ ] `internal/document/userdocs_test.go`: with `VDE_USER_DOCS=1`, report per file whether
      it is groundable and the text length (metadata only).

## Acceptance criteria

- `representation_test.go`: groundable text for `.txt`, `.md`, `.html` under both policies
  (visible text in both), and `.eml`. Not groundable for an image, for a PDF until 04-03, or
  for pages.
- A grounding test over `examples/samples/booking.html` and `booking.eml` with the sample
  schema and a correct candidate yields no entries.
- The redaction test passes with `GroundText` in its list.

## Out of scope

The PDF text layer (epic 04), OCR.

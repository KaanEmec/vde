# Job: Context header and page markers

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: in progress · Depends on: 01

Implementation done; comparison pending (orchestrator).

## Goal

Start every document the models read with one line of context: the kind of document,
when it was sent when that is known, and the page count. Mark every page, in every
representation that has pages, so dates, years and time zones can be resolved and
feedback can point at a place.

## Context

An email's text already begins with its header block (`internal/document/email.go`,
`headerBlock`: Date, From, Subject …). A PDF's text layer is joined with `--- page N ---`
markers (v0.4 job 04-02). Rendered pages and caller pages reach the model as image parts
with no text between them, and a text or HTML document says nothing about itself.

Proposed line, built only from what the service knows about the document:
`type: email · sent: 2026-10-10T09:14+02:00 · pages: 3`. The `sent` value is the email's
Date header, normalised to RFC 3339 with the original offset; it is left out for other
types (PDF metadata dates are unreliable). The job's creation time is not the document's
date and is never used.

## Tasks

- [x] `internal/document`: build the context line per representation: type (email, HTML,
      text, Markdown, PDF, image), `sent` for emails with a parseable Date header, `pages`
      for PDFs and page images.
- [x] Put the line first in the text representation. For `Pages` and `Native`, put a text
      part with the line before the first page.
- [x] For `Pages`, a text part `--- page N ---` before each page image.
- [x] Grounding: the context line and the page markers are not grounding text (check
      `GroundingText` and the email text path). A value that appears only in the context
      line must not be grounded.
- [x] The representation's policy string (hashed into `service_version`) names the
      change.
- [ ] `docs/api/writing-instructions.md`: tell callers the line exists and what it holds.
- [ ] Run the comparison, record it, keep or revert.

## Acceptance criteria

- Tests per document type: the context line's content; no `sent` when the Date header is
  missing or unparseable; page markers in order for rendered and caller pages.
- `internal/ground` test: a candidate value found only in the context line is a miss.
- `version_test.go`: `service_version` changes.
- The comparison is recorded with the keep or revert decision.

## Out of scope

A caller-supplied reference date (a `/v1` addition; would need the contract owner),
PDF metadata, language detection.

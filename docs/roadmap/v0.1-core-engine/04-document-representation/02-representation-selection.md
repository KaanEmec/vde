# Job: Representation selection

Version: v0.1 · Epic: 04 Document representation · Status: done · Depends on: 01, 03, 04, 02-06, 03-01

## Goal

Decide once, at submit, what both models read, from the document's type, the supplied
pages and the groups' declared capabilities. Refuse early when nothing can read it. The
service never produces a representation the caller did not supply, other than text
derived from HTML or email.

## Context

Contract §4, "Which representation the models read": native document when an extractor
endpoint accepts the type, otherwise page images, text and HTML as text; the judge sees
the same representation from the same bytes; when no endpoint in either role's group can
read any supplied representation, refuse at submit with `415 unsupported_document`. The
choice is part of `service_version`. ADR 0006 (raw HTML default, no server-side
rendering). Decisions 7, 8 and 13.

## Tasks

- [x] `Representation` type: one of `native` (document part), `pages` (image parts in
      order), `text` (text part), with the media types the router must filter on.
- [x] `Select(doc, extractorGroup, judgeGroup, policy) (Representation, error)`:
  1. plain text and markdown → `text`; HTML → `text` carrying the raw HTML (default
     policy `raw_html`) or the visible text (policy `visible_text`, job 03); email →
     `text` built by job 04 (headers plus body under the HTML policy);
  2. PDF or image: if any extractor pair accepts the media type → `native`; else if
     pages were supplied and any extractor pair accepts PNG or JPEG → `pages`; else
     error `unsupported_document`. No rendering step exists.
  3. the judge group must also be able to read the chosen representation; if not, try
     the next option in the same order; if none fits both roles → `unsupported_document`.
- [x] The representation name and the HTML policy feed the `service_version` hash
      (epic 05 job 07): `Kind.String()` and `HTMLPolicy.String()` are the names to hash.
- [x] The loop uses the selected representation to build identical document parts for
      both roles; a test asserts byte-equality of the parts seen by the fake provider in
      both roles. (Asserted with a recording `provider.Provider` in the test until the
      fake provider lands; the loop must pass `Representation.Parts` to both roles.)

## Acceptance criteria

- Table tests for: PDF with native-capable extractor; PDF without native but with pages;
  PDF without pages and without native capability (refused); image; HTML under both
  policies; email; judge group that cannot read native PDF forcing pages for both.

## Out of scope

Rendering pages (never, ADR 0006), choosing different representations per role (the
contract forbids it), image conversion (v0.3).

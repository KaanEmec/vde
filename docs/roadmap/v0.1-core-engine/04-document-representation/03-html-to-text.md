# Job: HTML policy: raw by default, visible text option

Version: v0.1 · Epic: 04 Document representation · Status: planned · Depends on: 01

## Goal

Implement the two HTML policies: `raw_html` (default) sends the HTML source as the text
part after safety normalization; `visible_text` reduces it to the text a reader would
see, in reading order, with link targets kept.

## Context

Decision 13 and ADR 0006: raw HTML by default, `visible_text` as a per-application
option, policy part of `service_version`. Contract §4 allows either.

## Tasks

- [ ] `raw_html`: decode to UTF-8 using the declared charset or a `<meta charset>`;
      strip `script` and `style` element contents and comments (no model benefit, token
      cost, injection surface); otherwise pass the source through unchanged. Cap at the
      document size limit.
- [ ] `visible_text`: parse with `golang.org/x/net/html`; drop `script`, `style`,
      `head` (keep `title`), hidden elements by the obvious attributes, comments; emit
      block elements as paragraphs, list items with a marker, table rows as
      tab-separated cells, `br` as newlines; links as `text (href)` for `http(s)` and
      `mailto`; images as `alt`; collapse whitespace; cap output (default 1 MiB) with a
      truncation marker.
- [ ] Policy resolution: `documents.html` default, overridable per application in v0.2
      (`applications[].documents.html`).
- [ ] Golden tests with real redacted HTML email bodies from the TravelInbox corpus
      (`testdata/travelinbox/`, decision 11) plus a few synthetic cases, for both
      policies.

## Acceptance criteria

- Golden tests pass; deterministic output for the same input under each policy.
- For a layout-table email the `visible_text` output reads top to bottom, left to right.

## Out of scope

Rendering HTML to an image, CSS-aware layout, JavaScript.

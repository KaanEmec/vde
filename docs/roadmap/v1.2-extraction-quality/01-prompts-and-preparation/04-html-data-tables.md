# Job: HTML data tables as Markdown

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: planned · Depends on: 03

## Goal

Under `documents.html: visible_text`, write data tables as Markdown tables with their
header row, so each value stays tied to its column (fare, tax, total; outbound, return).
Layout tables, which most email templates are built from, stay flattened as now.

## Context

`internal/document/html.go` (`visibleText`) separates table cells with tabs, which loses
the header association. A layout table converted to Markdown would be worse than tabs, so
the job is mostly the classifier: a table is a data table when it has `<th>` cells, or
when it has no nested table, at least two rows and two columns, and the same number of
cells in every row (counting `colspan`). Everything else is a layout table.

## Tasks

- [ ] `html.go`: classify each table as data or layout with the rule above.
- [ ] Data tables: a Markdown table with a header row (the `<th>` row, else the first
      row), cells with `|` and newlines escaped, `colspan` repeated as empty cells.
- [ ] Layout tables: unchanged output.
- [ ] Respect `maxVisibleText`; a table cut by the limit ends at a row boundary.
- [ ] Grounding: Markdown pipes and rule rows add no values; check the existing HTML and
      email samples ground as before.
- [ ] Synthetic fixtures: a fare breakdown with `<th>`, an itinerary table without `<th>`,
      a layout template with nested tables, a data table inside a layout table, a table
      with `colspan`.
- [ ] `docs/operator/configuration.md` (`documents.html`): describe the table output.
- [ ] Run the comparison, record it, keep or revert.

## Acceptance criteria

- Table-driven tests over the fixtures: data tables as Markdown, layout tables byte-equal
  to today's output.
- `FuzzHTMLText` runs clean.
- `version_test.go`: `service_version` changes under `visible_text` only; `raw_html` is
  unaffected.
- The comparison is recorded with the keep or revert decision.

## Out of scope

Tables in PDFs (the text layer has no table structure; see the backlog item on hybrid PDF
input), `raw_html`.

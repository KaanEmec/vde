# Job: Email section labels

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: planned · Depends on: 02

## Goal

Label the parts of an email body that are not the sender's own message, so the model
knows which part is current: a forwarded message and a quoted earlier reply each get a
line such as `[forwarded message]` or `[quoted earlier reply]` before them and
`[end of forwarded message]` after. Nothing is removed.

## Context

`internal/document/email.go` turns the first HTML (else plain-text) body into text. Travel
bookings often arrive forwarded, so the forwarded part is frequently the one that holds
the data; reply-stripping libraries (Talon, email-reply-parser) remove it, which would
lose the booking. Labels keep the content and add the structure.

Markers to detect, in HTML before text conversion and in plain text:

- forwarded: Gmail's `gmail_quote` with "Forwarded message", "Begin forwarded message:"
  (Apple Mail), Outlook's "From: … Sent: … To: … Subject:" block after a rule line,
  `-----Original Message-----`;
- quoted reply: `blockquote type="cite"`, Gmail's `gmail_quote` with "On … wrote:", lines
  starting with `>` in plain text.

Detection is heuristic and must fail safe: an undetected section stays unlabelled text, as
today; a false label never removes text.

## Tasks

- [ ] `internal/document`: detect forwarded and quoted sections in an email's HTML body
      (on the parsed tree, before `visibleText`) and in a plain-text body; nested forwards
      get nested labels, bounded in depth.
- [ ] Insert the label lines; keep every other byte of text as it is now.
- [ ] Grounding: label lines carry no values; check that grounding still finds every value
      it found before on the existing email samples.
- [ ] Synthetic fixtures under `internal/document/testdata` (no real mail): Gmail,
      Outlook and Apple Mail forwards, a reply with a quote, a forward inside a reply, a
      plain-text `>` quote, an email with none.
- [ ] `docs/api/writing-instructions.md`: callers can refer to the labels in instructions
      (for example "use the forwarded message for the booking").
- [ ] Run the comparison, record it, keep or revert.

## Acceptance criteria

- Table-driven tests over the fixtures: the expected label lines, and the text without
  labels equal to today's output for every fixture.
- `FuzzHTMLText` still runs clean; add a fuzzer over email parsing if none covers the
  labelling.
- `version_test.go`: `service_version` changes.
- The comparison is recorded with the keep or revert decision.

## Out of scope

Removing quoted text or signatures, reading attachments, threading several emails.

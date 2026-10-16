# Job: Email documents

Version: v0.1 · Epic: 04 Document representation · Status: done · Depends on: 01, 03

## Goal

Accept `message/rfc822`: parse the message, present the meaningful headers and the best
body as the text representation, and list attachments on the result so the caller can
submit the ones it wants as separate jobs.

## Context

Brief: "convert an image, a pdf, an email into a structured set of data." Decision 7
(core type from v0.1). ADR 0006. TravelInbox splits emails itself and never sends this
type, so nothing here affects the contract; it is a superset media type.

## Tasks

- [x] Parse with `net/mail`, `mime/multipart`, `mime/quotedprintable` and base64;
      charset decoding with `golang.org/x/text`; walk `multipart/alternative`,
      `multipart/mixed` and `multipart/related` trees.
- [x] Body selection: prefer `text/html` (then apply the HTML policy of job 03), else
      `text/plain`; a message with neither → `unsupported_document`.
- [x] Text representation: a short header block (`From`, `To`, `Cc`, `Date`, `Subject`,
      decoded per RFC 2047, plus `Message-ID` and `In-Reply-To` when present, for
      threading) followed by the body. Nothing else from the headers (no
      `Received` chains, no tracking headers).
- [x] Attachments: not read in v0.1. The job result gains an optional
      `attachments: [{name, media_type, bytes}]` list (metadata only) so the caller can
      decide what to submit as its own job. Inline images referenced by `cid:` are
      listed too and otherwise ignored.
- [x] Limits: the document limit applies to the whole message; a nested message
      (`message/rfc822` attachment) is listed as an attachment, not descended into.
- [x] Fixtures: `.eml` files from the TravelInbox corpus where available (decision 11)
      plus synthetic ones: HTML and text alternative, text only, nested multipart with a
      PDF attachment, non-UTF-8 charset, RFC 2047 encoded subject.
- [ ] `vde extract` accepts `.eml` (media type inferred) and prints the attachment list.
      Moved to epic 06 job 01 (the command lives in `cmd/vde`); `Document.Attachments`
      is ready for it.

## Acceptance criteria

- An `.eml` booking confirmation extracts to the same result as its HTML body submitted
  directly (scenario test with the fake provider asserting identical text parts).
- Attachments are listed with correct names, types and sizes.

## Out of scope

Reading attachments into the same job (a multi-document job is in the backlog),
`text/calendar` parts as structured input, S/MIME and PGP.

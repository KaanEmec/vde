# ADR 0006: Representation policy, raw HTML by default and no server-side rendering

Status: accepted (planning), 2026-10-16

Amended by [ADR 0010](0010-pdf-preprocessing-in-service.md) (PDF preprocessing in the service).

## Context

The TravelInbox contract leaves two choices to the service: whether HTML is given to the
model as HTML or as visible text, and whether the service renders PDF pages itself when
no configured model reads PDFs. The owner answered both.

## Decision

- **HTML is sent raw by default**, as a text part containing the HTML source. A
  per-application option `visible_text` reduces it to readable text in reading order with
  link targets, for models that handle HTML poorly or to save tokens. The policy in effect
  is part of `service_version`.
- **The service never renders PDF pages.** Operators configure a PDF-capable model for the
  extractor and judge groups, or callers send page images (`page` parts) as the contract
  allows. A PDF that no configured endpoint of either role can read, and that comes
  without pages, is refused at submit with `415 unsupported_document`.
- Image conversion (HEIC and WebP to JPEG, downscaling) remains a v0.3 option because it
  is cheap, pure Go, and does not change what the model sees semantically.
- Email (`message/rfc822`) is parsed in the service from v0.1: headers that carry meaning
  plus the best body (HTML, under the HTML policy above, else plain text) become the text
  representation; attachments are listed on the result for the caller to submit as
  separate jobs.

## Consequences

- No rasterizer dependency; the build stays cgo-free and the image small.
- `config check` warns when the extractor group cannot read `application/pdf` natively,
  since such a deployment depends on callers sending pages.
- Raw HTML can be large; the document size limit bounds it, and operators who care about
  tokens switch the application to `visible_text`.

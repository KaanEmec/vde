# Epic 04: Document representation

Version: v0.1 · Status: done· Depends on: 01, 02 (capabilities)

The document model, the media types the service accepts (including email), and the
decision of what the models actually read: the document bytes, the page images, or text.
Decided from configuration at submit time, identical for extractor and judge. The service
never renders pages itself (ADR 0006).

| # | Job | Status |
| --- | --- | --- |
| 01 | [Document model and media types](01-document-model-and-media-types.md) | done |
| 02 | [Representation selection](02-representation-selection.md) | done |
| 03 | [HTML policy: raw by default, visible text option](03-html-to-text.md) | done |
| 04 | [Email documents](04-email-documents.md) | done |

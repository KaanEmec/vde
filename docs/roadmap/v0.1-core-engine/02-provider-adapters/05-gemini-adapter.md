# Job: Google Gemini adapter

Version: v0.1 · Epic: 02 Provider adapters · Status: planned · Depends on: 01

## Goal

Native Gemini API adapter with PDF and image input, mapped to the provider interface,
with the strictest data-handling settings and a data-handling document.

## Context

Decision 3 (Gemini in the first release). Google's OpenAI-compatible endpoint does not
expose document input the same way, so Gemini gets its own small package. Use the current
API documentation when implementing; do not rely on memory for request fields or model
ids.

## Tasks

- [ ] `generateContent` mapping: system instruction; one user content with text parts
      and inline data parts (base64) for PDF and images in order; `maxOutputTokens`,
      low temperature; optional `responseMimeType: application/json` behind
      `json_mode: true`.
- [ ] Auth with an API key header from the resolved secret; optional extra headers.
- [ ] Error mapping to retryable and permanent kinds; `Retry-After` for 429; safety
      blocks reported as permanent with a distinct message (→ `extractor_failed`).
- [ ] Data handling (decision 12): document that paid-tier usage has different retention
      and training terms than the free tier and how the operator verifies which applies;
      set any request-level options that exist.
- [ ] `docs/providers/gemini.md`; `httptest` tests; opt-in live smoke test.

## Acceptance criteria

- Live smoke test passes with a key for a PDF, an image and a text document.

## Out of scope

Vertex-hosted Gemini (v0.3), the file upload API for very large documents.

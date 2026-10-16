# Job: Anthropic adapter

Version: v0.1 · Epic: 02 Provider adapters · Status: planned · Depends on: 01

## Goal

Call the Anthropic Messages API with text, image and native PDF document parts, map its
errors to the provider error kinds, and document what is sent.

## Context

ADR 0004. Anthropic models read PDFs and images natively, which makes this the reference
adapter for the "give the model the document" path. Use the current API documentation
(`claude-api` skill) when implementing; do not rely on memory for request fields, limits
or model ids.

## Tasks

- [ ] Request mapping: `System` → system prompt; `Parts` → one user message with text,
      image (base64) and document (base64 PDF) content blocks in order; `MaxTokens`,
      `Temperature` (default low, 0 or 0.1, for extraction).
- [ ] Headers: API key from the resolved secret, API version header, user agent.
      Optional extra headers from endpoint config (for proxies and gateways).
- [ ] Error mapping: 429 and 5xx and overloaded → retryable with `Retry-After` when
      present; 400 invalid request, 401, 403, 413 → permanent; refusal stop reason →
      permanent with a distinct message so the loop reports `extractor_failed`.
- [ ] Respect per-call timeout from the endpoint config via context.
- [ ] Data handling (decision 12): set every retention or no-training option the API
      exposes to its strictest value by default; document the account-level settings
      (zero data retention agreements) that cannot be set per request.
- [ ] Prompt caching for the fixed framing and the document on retries is an optional
      follow-up; note it in the adapter doc, do not implement in v0.1.
- [ ] `docs/providers/anthropic.md`: what is sent (framing, instructions, schema, document
      bytes, candidate, feedback), which headers, data retention and training settings
      the operator should know about and where to set them (contract §10).
- [ ] Tests with `httptest.Server`: successful call with PDF part, 429 with Retry-After,
      500, refusal; assert the JSON body shape.

## Acceptance criteria

- With a real key (opt-in test, env-gated) a one-page PDF produces a text answer.
- The data-handling document exists and is linked from the operator docs index.

## Out of scope

Batch API, streaming, tool use, extended thinking.

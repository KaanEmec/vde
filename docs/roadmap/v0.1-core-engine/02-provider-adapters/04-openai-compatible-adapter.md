# Job: OpenAI-compatible adapter with OpenRouter preset

Version: v0.1 · Epic: 02 Provider adapters · Status: planned · Depends on: 01

## Goal

One adapter for every endpoint that speaks the chat completions API: OpenAI, Azure OpenAI,
OpenRouter, Ollama, vLLM, LM Studio and similar. Images are sent as data URLs; PDFs only
when the endpoint-model pair declares `application/pdf` in `accepts`.

## Context

ADR 0004. Local and self-hosted models matter for an open-source, self-hosted service and
for operators who must keep documents on their own infrastructure. The adapter must not
assume any OpenAI-specific extension works everywhere.

## Tasks

- [ ] Request mapping: `System` → system message; `Parts` → one user message with
      `text` and `image_url` (data URL, base64) content parts; a `Document` part is sent
      with the endpoint's declared document input form (OpenAI `file` content part with
      base64 data when declared) or refused as permanent error if the pair does not
      declare the media type (the router never selects such a pair for a PDF; this is a
      safety net).
- [ ] Optional `response_format: {type: json_object}` when the endpoint config sets
      `json_mode: true`; off by default because many compatible servers reject it.
- [ ] Auth: `Authorization: Bearer` from the resolved secret when present; Azure-style
      `api-key` header and query `api-version` through endpoint config `auth_style` and
      `extra_query`; extra headers (OpenRouter attribution) from config.
- [ ] Error mapping like the Anthropic adapter; treat connection refused and DNS failures
      as retryable (local endpoints restart).
- [ ] `preset: openrouter` (decision 3): sets the base URL, the attribution headers and
      OpenRouter's provider-routing and data-policy request fields to the strictest
      values (no training, providers that honour zero retention only); covered by its
      own `httptest` test and the live smoke test. Other presets (`azure`, `ollama`) are
      small header or path tweaks in the same table.
- [ ] Data handling (decision 12): for OpenAI set the no-store/no-training request
      options the API exposes; document per hosted provider what is account-level only;
      local endpoints keep data local by construction.
- [ ] `docs/providers/openai-compatible.md`: what is sent, known differences between
      OpenAI, Azure, OpenRouter, Ollama and vLLM, retention and training notes per hosted
      provider, and that local endpoints keep data local.
- [ ] Tests with `httptest.Server` for OpenAI shape and for an Ollama-style minimal server.

## Acceptance criteria

- Opt-in tests pass against OpenAI, OpenRouter (with keys) and against a local Ollama
  vision model with an image document.
- The data-handling document exists.

## Out of scope

The Responses API, assistants, streaming. Gemini has its own adapter (job 05).

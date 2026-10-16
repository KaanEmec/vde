# ADR 0004: Provider adapters, Anthropic, OpenAI-compatible and Gemini first

Status: accepted (planning), 2026-10-16; amended the same day after the owner's answers

## Context

Document reading is LLM-first: the model receives the PDF or image directly. Operators
want hosted providers, local endpoints and failover between them. Each endpoint-model pair
accepts a different set of media types. The owner wants Anthropic, OpenAI-compatible
endpoints (including OpenRouter) and Google Gemini available in the first release, with
the strictest data-handling settings each provider offers.

## Decision

- One Go interface, `provider.Provider`, with a single method that takes a role request
  (system framing, user content parts: text, image, document; model name; limits) and
  returns the text answer plus usage, or a typed error classified as `retryable`
  (connection, 5xx, 429, timeout) or `permanent` (400, auth, content refused).
- Three adapters in v0.1: **Anthropic Messages API** (native PDF and image input),
  **OpenAI-compatible chat completions** (images; PDF only where the endpoint declares
  it), which also covers OpenAI, Azure OpenAI, OpenRouter, Ollama, vLLM and LM Studio
  through a base URL and header settings, and **Google Gemini** (native PDF and image
  input). OpenRouter is a named, tested configuration preset of the OpenAI-compatible
  adapter, not a separate package. A fourth adapter, **fake**, is scripted from a test
  file and is the basis of the whole test suite.
- Data handling: each adapter sets the strictest retention and no-training options the
  provider's API exposes, by default, and documents which settings exist only at account
  level. Operators approve endpoints knowingly (contract §10).
- Capabilities are configured, not probed: each endpoint-model pair lists the media types
  it accepts. `config check` verifies both roles can read the MUST media types through
  some endpoint or through page images.
- Structured output modes are not required; the loop parses text and the schema validator
  does the checking. An adapter may use a JSON mode as an optimization.
- The judge role may later be served by a **classifier-style provider** (for example
  TypeSafe AI's Jev, which returns typed decisions with calibrated probabilities instead
  of text). Such a provider yields a verdict and a confidence but no pointer-level
  feedback, which the contract requires on `reject`. It is planned as a v0.3 adapter with
  an explicit feedback fallback; the verdict type carries an optional `confidence` from
  v0.1 so no interface change is needed then.

## Consequences

- Adding a provider is one package implementing one method plus a capability declaration
  and a data-handling document.
- Bedrock and Vertex are v0.3 adapters, added when a user needs them.

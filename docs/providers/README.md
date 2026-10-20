# Provider adapters

`vde` ships five adapters for real model endpoints, one for classifier judges (deprecated, removal in 2.0) and one scripted fake for tests. Each
endpoint in the configuration names one adapter with `provider:`. Only endpoints you list in
the configuration ever receive a document (contract section 10), so read the retention
notes of an adapter before approving an endpoint.

## Comparison

| Adapter (`provider:`) | Document input | Image input | Text | Local or hosted | Retention and data handling |
| --- | --- | --- | --- | --- | --- |
| `anthropic` | PDF natively (`document` block) | PNG, JPEG, WebP | yes | Hosted (Anthropic API; `base_url` may point at a gateway) | [anthropic.md](anthropic.md) |
| `openai_compatible` | PDF as a `file` part, only on models whose `accepts` lists `application/pdf`; most local servers cannot | PNG, JPEG, WebP (`image_url` parts) on vision models | yes | Either: OpenAI, Azure OpenAI and OpenRouter are hosted; Ollama, vLLM and LM Studio are local | [openai-compatible.md](openai-compatible.md#retention-and-training-per-target) |
| `gemini` | PDF natively (`inlineData`) | PNG, JPEG, WebP, HEIC (`inlineData`) | yes | Hosted (Google Gemini API) | [gemini.md](gemini.md#data-handling-adr-0004-decision-12) |
| `bedrock` | PDF, text, HTML and Markdown as `document` blocks (Converse) | PNG, JPEG, WebP (`image` blocks) | yes | Hosted in your AWS account and region (credentials from the AWS default chain, no key) | [bedrock.md](bedrock.md#data-handling-adr-0004-decision-12) |
| `vertex` | PDF natively: Gemini as `inlineData`, Claude as a `document` block | PNG, JPEG, WebP (Claude, Gemini; HEIC Gemini only) | yes | Hosted in your Google Cloud project and region (application default credentials, no key) | [vertex.md](vertex.md#data-handling-adr-0004-decision-12) |
| `classifier` (deprecated) | No (text only) | No | yes | Either: a classifier service or a proxy implementing the classify contract; judge role only; deprecated in 1.1, removal in 2.0 | [classifier.md](classifier.md#what-is-sent-and-where) |
| `fake` | Whatever the script is asked | Whatever the script is asked | yes | Neither: no network, tests only | [fake.md](fake.md) |

"Document input" is a model's ability to read a PDF file itself. A model without it gets a
PDF as its text layer or as page images, rendered by the service or sent by the caller
([PDF preprocessing](../operator/pdf.md)). The adapter sends whatever the configuration
allows; whether a model can actually read a media type is a property of the model, not of
the adapter, and you declare it in each model's `accepts` list
([declaring `accepts`](../operator/providers.md#declaring-accepts)).

Native structured outputs (the caller's schema as a decoding constraint on extractor
calls) are opt-in per model and mapped by `openai_compatible` only:
[structured-outputs.md](structured-outputs.md), with the spike on the owner's endpoint.

## Adding an adapter

An adapter is a package under `internal/provider/<name>/`. Its documentation lives in
`docs/providers/`: `<name>.md`, except that `openai` is documented in
`openai-compatible.md`, after the `provider:` value it serves. A test fails when a
provider package has no page.

# Provider adapters

`vde` ships five adapters for real model endpoints and one scripted fake for tests. Each
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
| `fake` | Whatever the script is asked | Whatever the script is asked | yes | Neither: no network, tests only | [fake.md](fake.md) |

"Document input" is a model's ability to read a PDF file itself. A model without it reads a
PDF only through page images that the caller supplies, because the service never renders
pages. The adapter sends whatever the configuration allows; whether a model can actually
read a media type is a property of the model, not of the adapter.

## You declare what each model reads

The `accepts` list on each model entry is the single source of truth for what is sent to it:

```yaml
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: ANTHROPIC_API_KEY
    models:
      claude-sonnet-4-5:
        accepts: [application/pdf, image/png, image/jpeg, text/plain, text/html]
```

`vde` does not probe a model to find out what it reads (a probe would have to send a
document), and model capabilities change often. You are responsible for the list: take it
from the provider's current documentation, and keep it conservative. Listing a type the
model cannot read makes jobs fail; leaving a type out only means `vde` uses another
representation or another model for it.

`vde config check` prints, for each role, every media type, how it is read (`native`,
`pages` or `text`) and through which endpoint-model pairs, and warns when a required type
cannot be read. The [`examples/`](../../examples/README.md) configurations carry commented
`accepts` entries for common models.

## Adding an adapter

An adapter is a package under `internal/provider/<name>/`. Its documentation lives in
`docs/providers/`: `<name>.md`, except that `openai` is documented in
`openai-compatible.md`, after the `provider:` value it serves. A test fails when a
provider package has no page.

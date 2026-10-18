# OpenAI-compatible endpoints

One adapter (`provider: openai_compatible`, package `internal/provider/openai`) serves every
endpoint that speaks the Chat Completions API: OpenAI, Azure OpenAI, OpenRouter, Ollama,
vLLM, LM Studio and similar. It calls `POST {base_url}/chat/completions`; `base_url` includes
the version path (`https://api.openai.com/v1`, `https://openrouter.ai/api/v1`,
`http://ollama:11434/v1`).

## What is sent

Each role call is one request to the endpoint you configured, and nothing else is sent
anywhere:

- `model`, `max_tokens` (4096 when the request sets none; the router sets it from `max_output_tokens`) and `temperature`.
- A `system` message with the role framing.
- One `user` message whose content parts follow the order of the request: instruction and
  schema text, the document representation, the candidate answer, reviewer feedback.
  Images go as `image_url` parts with a base64 data URL. A PDF goes as a `file` part with a
  base64 data URL, and only when you declared `application/pdf` in the model's `accepts`;
  otherwise pages are sent as images.
- `Authorization: Bearer <key>` when the endpoint's `api_key_env` resolves to a value. Local
  endpoints may have no key; then no header is sent.
- `User-Agent: vde/<version>`.

Document content, instructions and answers are never written to logs or error messages, and
redirects are not followed, so an endpoint cannot forward a document elsewhere. The adapter
does not send `store`, `user` or any telemetry field. Not supported in v0.1: Azure's
`api-key` header and `api-version` query, `response_format`, the Responses API, streaming.

## OpenRouter preset

`preset: openrouter` fills in `https://openrouter.ai/api/v1` and adds:

- headers `HTTP-Referer: https://github.com/kaanemec/vde` and `X-Title: vde` (attribution);
- body field `"provider": {"data_collection": "deny"}`, which asks OpenRouter to route only
  to upstream providers that do not store prompts or train on them.

## Retention and training, per target

This is a summary of what the vendors documented when this was written. Terms change;
verify each point in the provider's console and contract before approving an endpoint
(contract section 10). "API-level" means the request can set it, which this adapter does
where noted; "account-level" means it is a setting or agreement you must arrange yourself.

| Target | Training on API data | Retention | Where controlled |
|---|---|---|---|
| OpenAI | Not used for training by default. | Prompts kept up to about 30 days for abuse monitoring. Zero data retention is available only to approved accounts. Chat Completions `store` defaults to false, so nothing extra is stored. | Zero retention and data residency: account-level (organization or project settings, sales agreement). Nothing the adapter can set per request. |
| Azure OpenAI | Not used to train the base models. | Abuse monitoring may keep prompts and outputs up to about 30 days; "modified abuse monitoring" removes that by approval. | Account-level, per Azure resource and subscription. Region is set when the resource is created. |
| OpenRouter | Depends on the upstream provider that serves the call. | OpenRouter does not log prompts unless you enable it; upstream providers differ. | API-level: `provider.data_collection: deny`, which the preset sends. Account-level: privacy and logging settings, and a zero-data-retention-only routing filter that the adapter does not set. Verify them in the OpenRouter console. |
| Ollama | Local; nothing leaves your host. | The server keeps no documents; logs may hold request metadata. | Your host. Cloud-hosted Ollama models are a separate service with its own terms. |
| vLLM | Local; nothing leaves your host. | None by default; enabling request logging (`--enable-log-requests`) writes prompts to logs. | Your deployment. |
| LM Studio | Local; nothing leaves your host. | None kept by the server. | Your host. |

Local endpoints keep data local by construction, but only if you control the network path:
put TLS or a private network between this service and any endpoint on another machine, and
do not expose an unauthenticated server beyond it.

## Examples

OpenRouter:

```yaml
endpoints:
  openrouter:
    provider: openai_compatible
    preset: openrouter          # base_url defaults to https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
    timeout: 120s
    models:
      google/gemini-2.5-flash:
        accepts: [image/png, image/jpeg]
```

Ollama (vision model, no key):

```yaml
endpoints:
  ollama:
    provider: openai_compatible
    base_url: http://ollama:11434/v1
    timeout: 300s
    models:
      llama3.2-vision:
        accepts: [image/png, image/jpeg]
```

Plain `http://` on a host other than loopback is a configuration warning (documents cross the
network unencrypted); on a private container network, as here, it can be accepted.

## Known differences

- OpenAI and OpenRouter accept the `file` part for PDFs on models that support it. Most
  local servers do not; leave `application/pdf` out of `accepts` there.
- Some newer OpenAI models require `max_completion_tokens` instead of `max_tokens`. The
  adapter sends `max_tokens`, which every compatible server understands.
- Message content may come back as a string or as an array of text parts; both are read.
- A `content_filter` finish or an empty answer is a permanent failure. 408, 429 and 5xx
  and network failures (including connection refused, since local servers restart) are
  retryable, and `Retry-After` is honoured.

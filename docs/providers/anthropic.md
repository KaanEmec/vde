# Anthropic provider: what is sent and how it is handled

Status: v0.1 reference for the `anthropic` endpoint provider (`internal/provider/anthropic`).
Decisions: ADR 0004 (decision 12, data handling); contract section 10 (operator approval of
endpoints).

Read this before approving an Anthropic endpoint. It describes what `vde` sends, what it
sets, and what can only be set outside `vde`. Statements about Anthropic's own retention
and training policies are what was known when this was written; they can change. Verify
them in the Anthropic Console and in your agreement with Anthropic.

## What is sent

One HTTPS `POST {base_url}/v1/messages` per role call (extractor or judge). The request
body carries, in this order:

| Where in the request | Content |
| --- | --- |
| `system` | The role framing (the fixed system prompt of the extractor or judge). |
| One `user` message, content blocks in part order | The instructions, the schema, the document, the candidate (judge calls) and the feedback from the previous attempt (extractor retries). |

The document goes out as the source material for the model to read, in one of these forms:

- a **PDF** as a `document` block with a base64 source (`application/pdf`), so the model
  reads the file natively, including its page images;
- **page images** as `image` blocks with a base64 source (`image/png`, `image/jpeg`,
  `image/webp`; `image/gif` is accepted by the API but vde does not produce it);
- text representations (extracted text, visible text of HTML) as `text` blocks.

Also sent: the model name, `max_tokens` (4096 when the request sets none; the router sets it from `max_output_tokens`) and, only when the
caller sets one above zero, `temperature`. Nothing else: no tools, no streaming, no
`metadata` (so no end-user identifier), no thinking configuration, no prompt-caching
markers, no `service_tier`, no `inference_geo`.

Documents are sent inline in every call that needs them. They are not uploaded to the
Anthropic Files API, so no file object is created in your Anthropic workspace.

The same document bytes are sent again on every retry and on every judge call. A failover
to another endpoint sends them to that endpoint too; each endpoint is approved separately.

The API limits one request to 32 MB. Base64 adds a third to the document size, so a PDF
larger than about 24 MB cannot be sent as a document block; the API answers with HTTP 413,
which `vde` treats as a permanent failure of that endpoint for that document. Keep
`limits.document_bytes` below that if Anthropic is the only endpoint for a role.

## Headers and settings that are set

| Header | Value |
| --- | --- |
| `x-api-key` | The key read from the environment variable named by `api_key_env`. Never logged, never in an error message. |
| `anthropic-version` | `2023-06-01`. |
| `content-type` | `application/json`. |
| `user-agent` | `vde/<version>`. |

No `anthropic-beta` header is sent, so no beta feature is enabled. Redirects are not
followed, so a misconfigured or hijacked `base_url` cannot forward the document elsewhere.
Responses are capped at 8 MiB. The per-call `timeout` of the endpoint applies as a total
deadline.

`vde` has no endpoint setting for extra request headers in v0.1, so gateways that need
additional headers are not supported by this adapter yet.

`base_url` defaults to `https://api.anthropic.com`. Pointing it at a proxy or gateway sends
every document to that proxy as well; the approval above then applies to the gateway.

## Per-request retention and training options

The Messages API exposes no per-request switch for data retention or for model training.
There is therefore nothing for `vde` to set to a stricter value. What `vde` does instead is
avoid every optional feature that stores or processes data beyond the call: no Files API,
no prompt caching, no Batch API, no tools, no streaming, no `metadata.user_id`.

## Account-level settings (cannot be set by `vde`)

These are controlled by Anthropic and your agreement with it, not by any request field:

- **Training on your data.** Anthropic states that inputs and outputs sent through the
  commercial API are not used to train its models by default. Confirm the terms that apply
  to your organization.
- **Retention.** API inputs and outputs are retained by Anthropic for a limited period by
  default, and may be kept longer where a request is flagged for a policy or safety review.
  The period is set by Anthropic's policy and your agreement, not by `vde`.
- **Zero data retention (ZDR).** A zero-data-retention arrangement is agreed with Anthropic
  for an organization (or workspace) and applies to eligible API usage. It is not a
  request option. Ask your Anthropic account team whether it is available to you and which
  models and features it covers.
- **ZDR and some models.** Some models require 30-day retention and answer every request
  with `400 invalid_request_error` from an organization or workspace without it (documented
  for the Fable 5.x and Mythos 5.x families). If an approved endpoint suddenly returns 400
  for every call with a valid payload, check the model's retention requirement against your
  ZDR status before debugging `vde`.
- **Data location.** Where inference runs (`inference_geo`) is not set by `vde`; the
  Anthropic default applies. Regional options, where offered, are account or request
  choices to verify in the Console.
- **Workspaces and keys.** Use a dedicated workspace and API key for `vde` so that
  retention, spend limits and access can be set and revoked for it alone.

Operator checklist: verify in the Anthropic Console (and with Anthropic where you have an
agreement) the training setting, the retention period, whether ZDR applies to the models you
configure, and the workspace that owns the key. Where this document is silent or unsure, the
Console and your agreement are authoritative.

## Errors and what they never contain

Failures are classified for failover:

| Situation | Kind |
| --- | --- |
| HTTP 408, 429 (with `Retry-After` when sent), 5xx including 529 overloaded | retryable |
| Network error, connection reset, timeout, context deadline | retryable |
| HTTP 400, 401, 402, 403, 404, 413, other 4xx, an unfollowed 3xx | permanent |
| `stop_reason: "refusal"` | permanent |
| A response with no text | permanent |
| Unreadable or oversized (over 8 MiB) response | permanent |

Error messages are fixed text plus, for HTTP errors, the error type the API documents
(for example `rate_limit_error`). They never include the request, the document, the model's
answer, the API's free-text error message or the key.

If the answer was cut off at `max_tokens` the text received is returned as is, with
`Truncated` set; the verification loop then tells the extractor the answer was cut off and
asks for a more compact one. A refusal is reported so the job
can end as `extractor_failed` instead of retrying the same content.

## Not implemented in v0.1

- **Prompt caching** of the fixed framing and the document across retries would cut input
  cost, but cached prompt content is an extra storage mechanism to approve. Optional
  follow-up.
- Batch API, streaming, tool use, extended thinking.
- Temperature: current Claude models reject non-default sampling parameters. `vde` sends
  `temperature` only when the caller sets a value above zero, so keep the extraction
  temperature at zero (unset) for those models.

## Tests

`go test ./internal/provider/anthropic/` runs against a local test server and needs no key.
An opt-in test calls the real API with a one-page PDF:

```
VDE_LIVE_ANTHROPIC=1 ANTHROPIC_API_KEY=... go test -run Live -v ./internal/provider/anthropic/
```

Set `VDE_LIVE_ANTHROPIC_MODEL` to choose the model (default `claude-haiku-5-5`). It sends a
synthetic document, nothing confidential.

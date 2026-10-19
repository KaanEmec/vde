# Google Gemini provider

Adapter: `internal/provider/gemini`. Configure with `provider: gemini`.

```yaml
endpoints:
  gemini:
    provider: gemini
    base_url: https://generativelanguage.googleapis.com   # default, may be omitted
    api_key_env: GEMINI_API_KEY
    timeout: 120s
    models:
      gemini-example-model:        # use a current model id from Google's model list
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
```

Model ids and the media types a model accepts change; take both from Google's current
documentation. The adapter does not validate them.

## What the adapter calls

`POST {base_url}/v1beta/models/{model}:generateContent`, with the key in the
`x-goog-api-key` header (never in the URL, so it does not appear in proxy or access logs).
Redirects are not followed.

| vde request field | Gemini field |
|---|---|
| `System` | `systemInstruction.parts[].text` |
| text part | `contents[0].parts[].text` (role `user`, parts in order) |
| image and document parts | `contents[0].parts[].inlineData` (`mimeType`, base64 `data`) |
| `MaxTokens` (the model's `max_output_tokens`) | `generationConfig.maxOutputTokens` |
| always 0 | `generationConfig.temperature` |

The answer is the concatenated text of `candidates[0].content.parts`. Usage comes from
`usageMetadata`.
Documents are sent inline, so very large files are limited by the API's request size cap;
the Files API is not used (out of scope for v0.1). JSON mode and extra headers are not
configurable in v0.1: the config schema has no field for them and the loop parses text.

## Errors

| Situation | Kind |
|---|---|
| 408, 429, 5xx, network failure, timeout | retryable (`Retry-After` honoured on 429/5xx) |
| other 4xx (bad request, bad key, unsupported input) | permanent |
| `promptFeedback.blockReason`, finish reason `SAFETY`, `RECITATION`, `PROHIBITED_CONTENT`, `BLOCKLIST`, `SPII` | permanent, generic message |
| no candidate, no text, malformed 2xx body | permanent |
| caller cancelled the context | permanent |

Error messages are generic. They never contain the request, the document, the answer or
Google's error body.

## Data handling (ADR 0004, decision 12)

**What vde sends:** the role framing, the instructions and schema text, and the document
itself (PDF bytes or page images, base64) or its text representation, plus candidates and
feedback in later loop iterations. It sends the API key only in the header. vde sends no
request-level option that controls retention or training: the Gemini API exposes none, so
these controls live in the account and the terms that apply to the key.

**What to verify.** The statements below reflect Google's published terms as understood
when this document was written. Terms change; the operator must confirm the current
position before approving an endpoint (contract section 10).

- **Free tier (unpaid services).** Google's terms allow content submitted through free
  keys to be used to improve its products, including model training, and reviewed by
  humans. Do not use a free-tier key for confidential documents.
- **Paid tier (a Cloud project with billing enabled).** Google's terms state that content
  is not used to train models. Prompts and responses may still be logged for a limited
  period for abuse and policy monitoring and legal disclosure. Tier is a property of the
  project that issued the key, not of the key string.
- **How to check which applies.** In Google AI Studio, open the API keys page: it shows the
  project and its plan tier for each key. In the Google Cloud console, check that the
  project has an active billing account and that the Generative Language API is enabled on
  it. Then read the current "Gemini API Additional Terms of Service" and the data-logging
  section of the Gemini API documentation. Verify in the console; vde cannot detect the
  tier from the API.
- **Caching.** Gemini may cache inputs on its side (implicit context caching) to reduce
  cost. vde does not create explicit caches. Confirm retention of cached content in the
  current documentation.

### Google Cloud and Vertex AI

Stronger controls (data residency by region, customer-managed encryption keys, VPC Service
Controls, opt-out of caching, and abuse-monitoring exemption or zero data retention by
agreement with Google) belong to Vertex AI and the Cloud account, not to the Gemini
Developer API. Those are account-level settings the operator configures in the Google Cloud
console and in the contract with Google. The Vertex-hosted adapter is planned for v0.3; the
v0.1 adapter targets the Gemini Developer API only.

### Recommendation

For documents the operator may not share with a third party, use a paid-tier key from a
dedicated project, verify the terms and tier in the console, and keep a local endpoint in
the failover group for documents that must not leave the premises.

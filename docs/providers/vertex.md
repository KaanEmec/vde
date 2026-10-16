# Google Vertex AI provider

Adapter: `internal/provider/vertex`. Configure with `provider: vertex`.

```yaml
endpoints:
  vertex:
    provider: vertex
    project: my-gcp-project             # required; the Google Cloud project id
    region: us-east5                    # required; or "global"
    timeout: 120s
    models:
      gemini-example-model:             # a current Gemini model id from the Vertex model list
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
      claude-example-model@20260101:    # an Anthropic model enabled in Model Garden
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
```

There is no `api_key_env`. Credentials come from Google application default credentials,
found when the endpoint is built: the service account or `authorized_user` file named by
`GOOGLE_APPLICATION_CREDENTIALS`, the `gcloud auth application-default login` credentials,
or the metadata server of a Google Cloud runtime (GKE workload identity, Cloud Run, Compute
Engine). The adapter requests the `cloud-platform` scope and refreshes the token as needed.
Prefer an attached service account or workload identity over a key file. A key variable in
`api_key_env` is rejected by `vde config check`; a missing credential fails when `vde`
builds the endpoint, before any document is sent. A `base_url`, when set, replaces
`https://{region}-aiplatform.googleapis.com` (for example a Private Service Connect
endpoint).

Model ids and the media types a model accepts change; take both from Google's current
documentation. The adapter does not validate them.

## What the adapter calls

The model id decides the publisher. `gemini*` goes to Google, `claude*` to Anthropic; any
other id is refused before a call. Every call is `POST` with `Authorization: Bearer <token>`
(the token is never in the URL), and redirects are not followed.

### Gemini

`https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/google/models/{model}:generateContent`

The request and response are the same as for the [Gemini adapter](gemini.md): `System` is
`systemInstruction`, parts are `contents[0].parts[]` with images and documents as base64
`inlineData`, `MaxTokens` (4096 when 0) is `generationConfig.maxOutputTokens`, `Temperature`
is `generationConfig.temperature`. The answer is the concatenated text of
`candidates[0].content.parts`; usage comes from `usageMetadata`; the reported model is
`modelVersion` or the requested id. A block reason, or a finish reason of `SAFETY`,
`RECITATION`, `PROHIBITED_CONTENT`, `BLOCKLIST` or `SPII`, is a permanent failure; finish
reason `MAX_TOKENS` sets `Truncated`.

### Anthropic

`https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/anthropic/models/{model}:rawPredict`

The body is the Anthropic Messages API shape with `anthropic_version: "vertex-2023-10-16"`
and no `model` field (the model is in the URL; Anthropic model ids on Vertex may carry an
`@version` suffix):

| vde request field | Messages field |
|---|---|
| `System` | `system` |
| text part | `messages[0].content[]` `text` block (role `user`) |
| image part | `image` block with a base64 source |
| document part, `application/pdf` only | `document` block with a base64 source |
| `MaxTokens` (4096 when 0) | `max_tokens` |
| `Temperature`, only when above zero | `temperature` |

The answer is the concatenated text blocks; usage comes from `usage`; `stop_reason`
`max_tokens` sets `Truncated` and `refusal` is a permanent failure. Streaming, tools,
thinking and caching markers are not sent.

For `region: global` the host is `aiplatform.googleapis.com` and the location is `global`.

## IAM

The service account or user needs permission to call the models: the predefined role
`roles/aiplatform.user` (it includes `aiplatform.endpoints.predict`) on the project, or a
custom role with that permission. The Vertex AI API (`aiplatform.googleapis.com`) must be
enabled on the project. Anthropic models must also be enabled for the project in Vertex AI
Model Garden (an agreement step in the console), and may need quota in the chosen region. A
403 is a permanent failure: it is not hidden by failover.

## Errors

| Situation | Kind |
|---|---|
| 408, 429, 5xx, network failure, timeout (`Retry-After` honoured) | retryable |
| token fetch timing out | retryable |
| other 4xx (bad request, no permission, model not enabled), token fetch rejected | permanent |
| block reason, blocked finish reason, refusal | permanent, generic message |
| no candidate, no text, malformed 2xx body | permanent |
| model id that is neither `gemini*` nor `claude*`, a text/HTML document part to a Claude model | permanent, before any call |
| caller cancelled the context | permanent |

Error messages are fixed text. They never contain the request, the document, the answer,
the access token or Google's error body.

## Data handling (ADR 0004, decision 12)

**What vde sends:** the role framing, the instructions and schema text, and the document
itself (PDF bytes or page images, base64) or its text representation, plus candidates and
feedback in later loop iterations. It sends them to the Vertex AI endpoint of the configured
project and region over TLS, signed only by the bearer token. vde sends no request-level
option that controls retention, caching or training.

**Content stays in your Google Cloud project and region.** Requests are made under your
project's identity to a regional endpoint; no Gemini Developer API key and no
Anthropic API key is involved, and no document goes to an Anthropic endpoint (Anthropic
models are served by Google). Things that can move or keep content, and that you control:

- **Region.** A regional `region` (for example `us-east5`, `europe-west4`) keeps processing
  in that region where the model is offered there. `region: global` lets Google serve the
  request from any location, so do not use it when data residency matters. Not every model
  is offered in every region; check the model's page in Model Garden.
- **Caching.** Google may cache Gemini inputs in memory to reduce latency (a project-level
  setting, with a default retention measured in hours); it can be disabled for a project.
  vde creates no explicit caches. Verify the current default and the setting for your
  project.
- **Abuse monitoring and logging.** Google may log prompts and responses for abuse
  detection unless an exemption applies, and request-response logging for a model can be
  switched on in the project (it writes to BigQuery). Verify the project's settings and
  whether an exemption is in place.

**What to verify.** The statements below reflect Google's published terms as understood
when this document was written. Terms change; the operator must confirm the current
position before approving an endpoint (contract section 10). Verify in the Google Cloud
console and the Vertex AI documentation ("Generative AI and data governance"):

- Google's terms state that customer content sent to Vertex AI is not used to train models
  without the customer's permission. Confirm the current wording, including for
  partner (Anthropic) models, which have their own terms in Model Garden.
- The retention, caching and abuse-monitoring position for the models you configure.
- Data residency commitments for the region (and the difference between regional and global
  endpoints), and controls available to you: customer-managed encryption keys, VPC Service
  Controls, Access Transparency, and zero-data-retention arrangements with Google.

### Recommendation

Use a dedicated project and service account with `roles/aiplatform.user` only, a regional
endpoint that satisfies your residency rules, caching disabled where documents are
confidential, and keep a local endpoint in the failover group for documents that must not
leave the premises.

## Tests

`go test ./internal/provider/vertex/` uses a local test server and a fake token source; it
needs no Google credentials. A live smoke test against a real project has not been run.

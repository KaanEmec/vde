# Providers: choosing models

`vde` calls model providers through adapters. This page covers the configuration decisions:
which models serve which role, what each model is declared to read, and how failover works.
What each adapter sends and what each provider retains is in the
[provider adapter pages](../providers/README.md); read the page of every adapter you enable
before you approve it, since only endpoints in your configuration ever receive a document.

## The model of the configuration

Three levels, all in the one YAML file ([key reference](configuration.md)):

```
endpoints   a provider account: adapter, base URL, key variable, models it serves
groups      an ordered failover list of endpoint-model pairs
roles       extractor and judge, each served by one group
```

```yaml
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: ANTHROPIC_API_KEY      # the NAME of the variable; the key never goes in the file
    models:
      claude-sonnet-4-5:
        accepts: [application/pdf, image/png, image/jpeg, text/plain, text/html]
groups:
  main:
    endpoints:
      - {endpoint: anthropic, model: claude-sonnet-4-5}
roles:
  extractor: {group: main}
  judge: {group: main}
```

The request cannot name an endpoint or a model: callers get whatever the roles are
configured to use.

| `provider:` | Use for | Key |
| --- | --- | --- |
| `anthropic` | Anthropic API | `api_key_env` required |
| `gemini` | Google Gemini API | `api_key_env` required |
| `bedrock` | Models on Amazon Bedrock (Converse API) | none: AWS default credential chain; `region` required |
| `vertex` | Gemini and Anthropic models on Google Vertex AI | none: Google application default credentials; `project` and `region` required |
| `openai_compatible` | OpenAI, Azure OpenAI, OpenRouter (`preset: openrouter`), Ollama, vLLM, LM Studio | `api_key_env` optional (local servers); `base_url` required without a preset |
| `fake` | Tests only | none |

### Cloud providers: Bedrock and Vertex AI

Use these when your organization already has a cloud account and wants cloud IAM instead of
API keys, and the document to stay inside that account and region. Neither takes
`api_key_env`: credentials come from the cloud's own chain, and `vde config check` rejects a
key variable. Model ids below are placeholders; use current ids from the provider's
console.

```yaml
endpoints:
  bedrock:
    provider: bedrock
    region: us-east-1                   # required
    models:
      anthropic.example-model-v1:0:
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
  vertex:
    provider: vertex
    project: my-gcp-project             # required
    region: us-east5                    # required; "global" lets Google pick the location
    models:
      gemini-example-model:             # gemini* ids go to Google, claude* ids to Anthropic
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
      claude-example-model@20260101:
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
```

Bedrock reads the AWS default chain (`AWS_PROFILE`, `AWS_ACCESS_KEY_ID` and friends, SSO,
or the role of the task, pod or instance); Vertex reads `GOOGLE_APPLICATION_CREDENTIALS`, the
`gcloud` application default login or the attached service account. Set these in the
service's environment, not in the YAML. Permissions, regions and retention terms are in the
[Bedrock](../providers/bedrock.md) and [Vertex AI](../providers/vertex.md) pages; read them
before approving either endpoint.

## Choosing models per role

- **Extractor.** Reads the document and produces the JSON. It needs the strongest reading
  ability you can afford: if a document type is read badly here, no judge can fix it.
  Native PDF and image input matters most for scanned or complex documents.
- **Judge.** Receives the same document and the candidate, and accepts or rejects it with
  feedback. Prefer a different model than the extractor, ideally from a different provider,
  so the judge does not share the extractor's mistakes. Using one model for both works (the
  development examples do) but the model then checks its own work. The judge can be cheaper
  than the extractor, but it must still read the document well enough to catch errors.
- **Cost.** One job costs at least one extractor call and one judge call per attempt, up to
  `max_attempts` (default 3); the judge is skipped on an attempt whose answer fails schema
  validation. Lower `limits.max_attempts` or an application's `max_attempts` to cap spend.
- **Confidential documents.** Choose endpoints whose retention and training terms you have
  checked, use paid-tier keys where the provider treats free tiers differently (the
  [Gemini page](../providers/gemini.md) says so), and consider a local endpoint (Ollama,
  vLLM) so nothing leaves your hosts. Every endpoint in a group can receive the document.

## Declaring `accepts`

Each model entry has an `accepts` list, and it is the only thing that decides what is sent
to that model. `vde` does not probe a model (a probe would have to send a document), and
model capabilities change often, so you own the list: take it from the provider's current
documentation and keep it conservative.

| Value | Meaning |
| --- | --- |
| `application/pdf` | The model reads PDF files itself. |
| `image/png`, `image/jpeg`, `image/webp`, `image/heic` | The model reads these images. (HEIC: Gemini adapter only.) |
| `text/plain`, `text/html`, `text/markdown` | The model reads text. Text, HTML and `.eml` documents are sent as text, so listing `text/plain` is enough for all of them. |

`image/webp` and `image/heic` need a decision. For WebP you do not have to declare it: with
`documents.images.enabled: true` (the default) `vde` converts a WebP to JPEG (or PNG, per
`convert_to`) when the two groups do not both read WebP, so a model that lists only
`image/png` and `image/jpeg` processes WebP documents. Declare `image/webp` only if you want
the model to receive the WebP unchanged (it is then used as is unless the image must be
downscaled). HEIC cannot be converted without cgo: list `image/heic` on the models of both
groups (Gemini adapter only) or HEIC documents are refused with `415`.

What a wrong list does:

| Mistake | Result |
| --- | --- |
| A type listed that the model cannot read | Submissions are accepted, then jobs fail: the provider refuses or garbles the request. Usually `extractor_failed`. |
| A type left out that the model can read | `vde` does not use the model for it: another pair in the group, or a different representation, is used; if none, `unsupported_document` (`415`) at submission. |

How a group reads a media type (shown by `vde config check`, per role):

| Via | Meaning |
| --- | --- |
| `native` | A pair of the group lists the type; the model receives the file. |
| `pages` | PDF only: no pair lists `application/pdf`, but one lists `image/png` or `image/jpeg`. The PDF works only if the caller sends page images (`page` parts), because the service never renders pages. |
| `text` | Text types: a pair lists `text/plain` or the type itself. |
| `-` | The group cannot read the type. |

`config check` warns when a role's group cannot read one of PNG, JPEG, PDF, HTML or plain
text (the types the service must accept), and when PDFs depend on caller-supplied pages.
Treat these warnings as real: `vde config check --strict` makes them errors, which suits CI.

Check the result before you serve:

```
vde config check --config vde.yaml
```

For models the project has not documented, test with a real document of each type using
`vde extract` before putting the model behind callers.

## Failover groups

A group lists endpoint-model pairs in order. For one role call, `vde` tries the first
healthy pair that can read the request's media types, then moves on when a pair fails in a
way that another pair or a later try might fix.

| Key | Default | Meaning |
| --- | --- | --- |
| `failover.max_tries` | 3 | Tries across the list for one call. |
| `failover.max_total_time` | `3m` | Bound on the whole call including failover. Must be less than `limits.job_timeout` (default `5m`, at most `8m`). |
| `failover.failover_on_permanent` | `false` | Also move to the next pair after a permanent failure (refusal, bad request) instead of failing the call. |

Rules the router follows:

- **Retryable failures** (network errors, timeouts, `5xx`, `408`, `429`) move to the next
  pair. A `Retry-After` of at most 10 seconds that fits in the remaining time is waited out
  and the same pair retried once.
- **Permanent failures** (other `4xx` such as `401` and `400`, refusals, unsupported input)
  are not retried or failed over, unless `failover_on_permanent` is set. A wrong key is
  permanent, so a group does not hide it by failing over.
- A pair that fails retryably is marked down for 30 seconds, doubling per consecutive
  failure up to 5 minutes, and is tried after the healthy ones; a success clears it. The
  mark is in memory, per process, and lost at restart.
- A pair is skipped for a request whose media types it does not list in `accepts`.
- Failover never crosses roles: the extractor group and the judge group are separate.
- `/healthz` reports a role as down only when every pair of its group is marked down.

A typical production layout is a primary and a fallback on different providers for each
role, and a judge group on different models than the extractor group:
[`examples/vde.failover.yaml`](../../examples/vde.failover.yaml). Put a pair that reads
fewer types later in the list, and remember that every endpoint in a group may receive the
document.

Provider calls are counted in [metrics](operations.md#metrics) by role, endpoint, model and
outcome (`vde_provider_calls_total`), and failovers as `vde_failovers_total`; use them to
see which pair is failing.

## Credentials

An endpoint names the environment variable that holds its key (`api_key_env`). The variable
must be set when the configuration is loaded, or validation fails with
`environment variable X is not set`. The key is read at start; to change one, restart the
service. `bedrock` and `vertex` endpoints have no key variable: their cloud credentials are
resolved by the AWS and Google libraries (Bedrock on the first call, Vertex when the
endpoint is built) and refreshed by them. Keys are never printed by `config check`, logged or returned by the API. In
compose, put the key in `.env`; with systemd, in an `EnvironmentFile` with mode `0600`.

## Provider pages

| Adapter | Page |
| --- | --- |
| `anthropic` | [anthropic.md](../providers/anthropic.md) |
| `openai_compatible` (OpenAI, Azure, OpenRouter, Ollama, vLLM, LM Studio) | [openai-compatible.md](../providers/openai-compatible.md) |
| `gemini` | [gemini.md](../providers/gemini.md) |
| `bedrock` | [bedrock.md](../providers/bedrock.md) |
| `vertex` | [vertex.md](../providers/vertex.md) |
| `fake` | [fake.md](../providers/fake.md) |

Common provider failures and how to read them are in
[troubleshooting](troubleshooting.md#provider-failures).

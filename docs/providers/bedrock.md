# Amazon Bedrock provider

Adapter: `internal/provider/bedrock`. Configure with `provider: bedrock`.

```yaml
endpoints:
  bedrock:
    provider: bedrock
    region: us-east-1                  # required; the AWS region that serves the calls
    timeout: 120s
    models:
      anthropic.example-model-v1:0:    # a Bedrock model id or inference profile id from your console
        accepts: [application/pdf, image/png, image/jpeg, text/plain]
```

There is no `api_key_env` and no `base_url` is needed. Credentials come from the AWS
default credential chain, resolved by the AWS SDK when the first call is made: the
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` variables, `AWS_PROFILE`
and the shared config (including SSO), a web identity token, or the role of the ECS task,
EKS pod or EC2 instance. Prefer a role: no long-lived key then exists on the host. A key
variable in `api_key_env` is rejected by `vde config check`. A `base_url`, when set,
replaces the service endpoint (for example an interface VPC endpoint for
`bedrock-runtime`).

Model ids and the media types a model accepts change; take both from the Bedrock
documentation and your console. The adapter does not validate them.

## What the adapter calls

`bedrockruntime.Converse` through the AWS SDK for Go v2 (SigV4 signed), once per role call.
The SDK's own retries are off: `vde` retries and fails over by the error kind below.

| vde request field | Converse field |
|---|---|
| `Model` | `modelId` |
| `System` | `system[0].text` |
| text part | `messages[0].content[].text` (role `user`, parts in order) |
| image part (`image/png`, `image/jpeg`, `image/webp`) | `content[].image` (`format`, raw bytes) |
| document part (`application/pdf`, `text/plain`, `text/html`, `text/markdown`) | `content[].document` (`format` `pdf`, `txt`, `html`, `md`; raw bytes) |
| `MaxTokens` (the model's `max_output_tokens`) | `inferenceConfig.maxTokens` |

Documents are named `document 1`, `document 2` and so on: Bedrock places the name in the
prompt, so a file name is never sent. Image and document bytes travel inside the request;
Bedrock limits their number and size per request (check the current quotas for your model).
HEIC images and other media types are refused by the adapter before any call, as a
permanent error, because the Converse API has no format for them.

The answer is the concatenated text blocks of the returned message. Token usage comes from
`usage.inputTokens` and `usage.outputTokens`. Converse does not report which model answered,
so the requested model id is reported. Anthropic models on Bedrock are reached through the
same Converse call; the adapter does not use the Anthropic Messages shape on Bedrock.

## IAM

The role or user needs `bedrock:InvokeModel` on the model (or inference profile) ARN it
calls; Converse is authorized by that action. Scope it to the models in your configuration:

```json
{
  "Effect": "Allow",
  "Action": "bedrock:InvokeModel",
  "Resource": [
    "arn:aws:bedrock:us-east-1::foundation-model/anthropic.example-model-v1:0"
  ]
}
```

An inference profile id (a model id with a geography prefix such as `us.` or `eu.`) needs
the profile ARN and the foundation-model ARNs in every region the profile routes to. Model
access must also be enabled for the account in the Bedrock console. A missing permission
or model access is `AccessDeniedException`, a permanent failure: it is not hidden by
failover, so a wrong role is visible at once.

## Errors

| Situation | Kind |
|---|---|
| `ThrottlingException`, `ServiceQuotaExceededException` | retryable |
| `ModelTimeoutException`, `ModelNotReadyException`, `InternalServerException`, `ServiceUnavailableException` | retryable |
| any other HTTP 408, 429 or 5xx, network failure, timeout | retryable |
| `ValidationException`, `AccessDeniedException`, other 4xx, unknown service errors | permanent |
| `stop_reason` `guardrail_intervened` or `content_filtered`; a response with no text | permanent |
| caller cancelled the context, no credentials found | permanent |
| `stop_reason` `max_tokens` | answer returned with `Truncated` set |

Error messages carry only a fixed text and the AWS error code (for example
`ThrottlingException`). They never contain the request, the document, the answer or the
service's free-text message, which can quote the request.

## Data handling (ADR 0004, decision 12)

**What vde sends:** the role framing, the instructions and schema text, and the document
itself (PDF bytes or page images, raw bytes in the signed request) or its text
representation, plus candidates and feedback in later loop iterations. It sends them to the
Bedrock runtime endpoint of the configured region over TLS. Redirects are not followed.
vde sends no guardrail configuration, no prompt-caching markers, no additional model
request fields and no request metadata.

**Content stays in your AWS account and region.** The call is made by your account's
credentials to the regional Bedrock endpoint; no API key for a third-party service is
involved and no document is sent to an Anthropic or other model-vendor endpoint. Two things
can still move or keep content, and you control both:

- **Region and cross-region inference.** The `region` key picks the endpoint. A model id
  that is an inference profile with a geography prefix (`us.`, `eu.`, a global profile) lets
  Bedrock route the request to another region inside that geography or globally. If data
  residency matters, use a model id that is served in the configured region only, or
  confirm the profile's regions in the console.
- **Model invocation logging.** It is off by default. If an administrator turns it on for
  the account and region, Bedrock writes prompts and responses, including document bytes,
  to the CloudWatch Logs group or S3 bucket they chose. Check the Bedrock settings page and
  the destination's retention and access policy.

**What to verify.** The statements below reflect AWS's published terms as understood when
this document was written. Terms change; the operator must confirm the current position
before approving an endpoint (contract section 10). Verify in the AWS console and the AWS
documentation:

- AWS states that Bedrock does not store prompts or completions and does not use them to
  train models or share them with model providers. Read the current "data protection" page
  of the Bedrock documentation and the service terms, and note any feature that stores
  data (such as model invocation logging, above, or model customization, which vde does
  not use).
- Whether abuse-detection or other provider-side processing applies to the model you chose,
  and in which region it runs. Third-party models are hosted in AWS-owned accounts; confirm
  the current statement for that model.
- That the model is available in your region, the quotas that apply, and any per-request
  size limits for images and documents.

### Recommendation

Use an IAM role scoped to the specific models, a region (or regional model id) that
satisfies your residency rules, keep model invocation logging off or pointed at a
restricted, short-retention destination, and keep a local endpoint in the failover group for
documents that must not leave the premises.

## Tests

`go test ./internal/provider/bedrock/` injects a fake `ConverseAPI` and also drives the real
SDK client against a local test server (checking path and SigV4); it needs no AWS
credentials. A live smoke test against a real account has not been run.

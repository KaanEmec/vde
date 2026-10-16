# Job: AWS Bedrock and Vertex AI adapters

Version: v0.3 · Epic: 02 More providers · Status: planned · Depends on: v0.1 02-01, 02-05

## Goal

Reach Anthropic and other models through cloud accounts operators already have, with
cloud IAM instead of API keys.

## Tasks

- [ ] Bedrock: Converse API with document and image blocks; SigV4 auth through the AWS
      SDK default credential chain; region from config. Anthropic models on Bedrock
      reuse the Anthropic request mapping where the API shape is the Messages API.
- [ ] Vertex AI: Gemini and Anthropic models with Google application default
      credentials.
- [ ] Both: data-handling documents stressing that content stays within the operator's
      cloud account and region.
- [ ] Decide per adapter whether to implement now or when a user asks; this job may be
      split or deferred without affecting anything else.

## Acceptance criteria

- At least one of the two passes a live smoke test before being announced.

## Out of scope

Azure OpenAI (covered by the OpenAI-compatible adapter).

# Verified Document Extraction — short project brief

**Updated:** 2026-10-15 · **Status:** Idea / pre-implementation · **Name:** Working title

## Purpose

Build an easily configurable, self-hosted Go backend that anyone can install on a server to turn unstructured documents into structured JSON. An extractor LLM reads the document, and a separately configured judge LLM checks the result. Expose an authenticated HTTP API usable by Swift apps, web applications, other backends, and any client that can make HTTP requests.

Example uses include extracting travel bookings, PNRs, dates, and locations, or saving lab results as structured observations. The backend is reusable across application domains; callers supply extraction instructions and an optional schema.

## Workflow

**Document → extractor → judge → accepted result, or feedback and another attempt.**

The extractor receives the document, task instructions, and optional schema. The judge checks the candidate against the original document for accuracy, completeness, and invented information. Rejected results are re-extracted with its feedback. Stop at a configured attempt limit and return an explicit unaccepted result when issues remain.

Document reading is LLM-first with models that support the supplied format. No OCR pipeline is planned initially. Without a predefined domain schema, instructions guide the JSON structure. Return JSON, acceptance status, final feedback, attempt count, and clear execution errors.

## AI endpoints, roles, and failover

Keep configuration easy while allowing hosted providers, local endpoints, and custom adapters:

- **Endpoint definitions:** Configure provider/protocol, base URL, credentials, supported models, and optional request settings once.
- **Endpoint groups:** Define a primary endpoint and ordered fallback endpoints for the same role or compatible model family. If a service is down or temporarily unavailable, route the call to a compatible fallback. Resolve endpoint-specific model names through configuration.
- **Independent roles:** Assign extractor and judge their own endpoint groups, model choices, and instructions. Roles may share a group or use different services.
- **Separate failure policies:** Endpoint failover handles service availability. Judge rejection drives extraction repair; optionally escalating to a stronger model is a separate setting. Bound retries and timeouts so endpoint failover cannot create an endless loop. A failed judge request never implies acceptance.

Illustrative configuration, not a finalized API or promise that providers are interchangeable:

```yaml
endpoints:
  primary: {provider: adapter_a, base_url: "https://ai-a.example", api_key_env: AI_KEY_A}
  backup: {provider: adapter_b, base_url: "https://ai-b.example", api_key_env: AI_KEY_B}
groups:
  extraction:
    endpoints:
      - {endpoint: primary, model: extraction_model_a}
      - {endpoint: backup, model: compatible_extraction_model_b}
  review:
    endpoints:
      - {endpoint: primary, model: review_model_a}
      - {endpoint: backup, model: compatible_review_model_b}
roles:
  extractor: {group: extraction, instructions: "Extract the requested information."}
  judge: {group: review, instructions: "Check accuracy and completeness against the source."}
max_attempts: 3
```

Validate configuration early, including document capabilities and allowed failover destinations. Only operator-approved endpoints are eligible to receive documents; clients must not be able to turn the service into an arbitrary AI proxy.

## Security and access tokens

Knowing the API URL must not grant access. Strong authentication and authorization are core requirements, enabled by default:

- Require HTTPS and valid credentials for every document-processing request and access to results. Deny requests with missing, invalid, expired, or revoked credentials before making AI calls.
- Support separately issued, scoped, expiring, and revocable access tokens for users or applications, with credential rotation. Keep token issuance and administrative actions protected. Exact token format and identity integration remain implementation choices.
- Keep AI provider credentials on the server. Swift and browser clients must not contain reusable server or provider secrets; use an appropriate user sign-in/token flow. Server-to-server clients can use separately managed application credentials.
- Enforce per-user/application ownership and permissions for documents and results, plus rate limits, upload limits, and AI usage quotas. Authentication alone must not grant unlimited usage or access to another caller's data.
- Protect stored documents and results, make retention configurable, and exclude credentials and document contents from routine logs. Record security-relevant access and administrative events without exposing secrets.

Here, token mechanisms refer to API access credentials. Sensitive-data masking/tokenization inside documents is a separate feature to consider if a consuming application needs it. Endpoint secrecy is not an access-control mechanism.

## First release and deployment

Ship a Go server binary and a straightforward container deployment, with documented configuration for roles, endpoints, failover, credentials, and access control. Provide a minimal authenticated HTTP API and request examples usable from Swift and web clients. Keep provider integration behind small Go interfaces.

Demonstrate extraction with and without a schema, service-outage failover independently for each role, and rejection of unauthorized requests before provider usage. Keep the initial workflow a small bounded loop; Temporal or another workflow engine remains optional. A review UI and domain-specific database integrations are outside the first release.

**Quality goal:** Improve extraction through review and correction. “99% correct” is an aspiration to measure on representative documents, not a guarantee derived from judge confidence.

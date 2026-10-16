# Verified Document Extraction: service contract, version 1

Date: 2026-10-16. Status: proposed, pending the first review with the service's developers. Owner on the TravelInbox side: `backend/internal/extract`.

This document is the agreement between TravelInbox and the document extraction service of ADR 0005 (working title "Verified Document Extraction", its own open-source repository, not yet created). It is written for the developers of that service, so that it can be built in parallel with TravelInbox and plugged in without either side reading the other's code. Everything TravelInbox's client sends and expects is here; the HTTP surface is also described, machine-readably, in `shared/extraction-service-api.yaml` (OpenAPI 3.1). Where the two disagree, this document wins and the YAML is fixed.

The service is domain-agnostic. Nothing in this contract is specific to travel except the examples; TravelInbox supplies all travel knowledge per request, as instructions and a JSON schema.

## 1. Division of responsibilities

| The service | TravelInbox |
| --- | --- |
| Reads a document with an extractor model and returns JSON conforming to a caller-supplied schema | Collects documents (email, upload, photo, paste), stores the originals, splits emails into body and attachments, renders page images |
| Checks the candidate with a judge model against the source for accuracy, completeness and invented values | Writes the extractor and judge instructions and the schema, and versions them |
| Retries extraction with the judge's feedback up to a bounded number of attempts | Validates the result again, turns it into reservations, sends unaccepted results to a human |
| Configures AI providers, endpoint groups, failover, models and their credentials | Holds exactly one service credential per environment and never talks to an AI provider |
| Enforces authentication, per-application quotas, rate limits, upload limits and retention | Backs off on quota, keeps originals itself, asks for zero retention on the service side |
| Reports a stable version string for the configuration that produced each result | Records that string, the attempt count and the full response with every document |

What the service must not do: hold or add domain knowledge of its own, alter the schema or the instructions it is given, return `accepted` for anything the judge did not accept, or forward a document to an endpoint the operator has not approved.

## 2. Conventions

- The words MUST, SHOULD and MAY are used as in RFC 2119. Everything marked MUST is what TravelInbox's client depends on; SHOULD and MAY items are requested but TravelInbox works without them.
- Base path `/v1`. Requests and responses are UTF-8 JSON unless a section says otherwise. Timestamps are RFC 3339 in UTC.
- HTTPS MUST be required for every request outside loopback. Plain HTTP is for `localhost` in development only.
- Clients ignore JSON fields they do not know. The service MAY add fields and error codes within `/v1`; it MUST NOT remove a field, change a field's type or meaning, or change the meaning of a status or error code without a new base path (section 11).
- Every response SHOULD carry `X-Request-Id`, echoing the client's header when one is sent, else a fresh id. It is the handle for a support conversation that must never involve document contents.
- Error bodies have one shape, section 9. Error messages MUST NOT quote document contents, instructions or extraction output.

## 3. Authentication and applications

The service knows **applications**. TravelInbox is one application per environment (`travelinbox-dev`, `travelinbox-staging`, `travelinbox-prod`). Each application has one or more **access tokens**.

- Every request to `/v1/*` MUST carry `Authorization: Bearer <token>`. A request with a missing, malformed, expired or revoked token MUST be refused with `401` before any provider call, any storage of the upload, and any quota accounting. A valid token used outside its scope MUST get `403`.
- Tokens are opaque strings. TravelInbox stores one in `EXTRACTION_TOKEN` and treats it as a secret. The service MUST store only a hash of each token.
- Tokens are issued and revoked by the operator, not through the API an application uses. TravelInbox needs: an expiry date on each token, several active tokens per application so one can be rotated in while the old one still works, and revocation that takes effect at once. A CLI such as `vde token create --app travelinbox-prod --scope jobs --expires 2027-10-16` printing the token once is enough; a protected admin API is fine too.
- Scopes: one scope, `jobs`, covering submit, poll and quota, is enough for TravelInbox. Finer scopes MAY exist.
- Ownership: a job is visible only to the application that created it. Another application's token gets `404`, not `403`, so job ids leak nothing.
- Browser and mobile clients never hold a service token. TravelInbox's worker is the only caller.

## 4. Submitting a job: `POST /v1/jobs`

Content type `multipart/form-data`. Parts, in any order:

| Part | Count | Content-Type | Content |
| --- | --- | --- | --- |
| `document` | exactly 1 | the document's media type, with parameters (`text/html; charset=utf-8`) | The document to read. Filename is arbitrary. |
| `page` | 0 to 20 | `image/png` or `image/jpeg` | A rendered page of the document, repeated, page 1 first, in request order. TravelInbox sends PNG at 150 dpi. None for documents without pages (text, HTML, a single image). |
| `extractor_instructions` | exactly 1 | `text/markdown` or `text/plain` | Instructions for the extractor role. |
| `judge_instructions` | exactly 1 | `text/markdown` or `text/plain` | Instructions for the judge role. |
| `schema` | exactly 1 | `application/schema+json` | JSON Schema, draft 2020-12, that the result MUST conform to. |
| `retention` | 0 or 1 | `text/plain` | `none` or `default`. Absent means `default`. Section 10. |
| `max_attempts` | 0 or 1 | `text/plain` | A positive integer. Lowers the attempt limit for this job below the application's configured limit; a higher value is clamped, not refused. TravelInbox does not send it. |

Document media types the service MUST accept: `application/pdf`, `image/jpeg`, `image/png`, `text/html`, `text/plain`. SHOULD accept: `image/webp`, `image/heic`, `text/markdown`. Anything else is `415`. TravelInbox never sends `message/rfc822`: an email arrives as its HTML or text body, and each attachment is a job of its own.

Limits the service MUST allow, and SHOULD let the operator raise:

| Item | Limit |
| --- | --- |
| `document` | 25 MiB |
| each `page` | 10 MiB |
| `page` count | 20 |
| each instructions part | 64 KiB |
| `schema` | 256 KiB |
| whole request body | 128 MiB |

Over any limit: `413`. The service MUST read the whole request before answering so that the client sees the status rather than a reset connection.

### Which representation the models read

The service MUST decide this itself from its configuration, which knows what each endpoint's models accept:

- When an endpoint in the extractor role's group accepts the document's media type natively, give the model the document. Otherwise give it the `page` images. A text or HTML document has no pages and is given as text; the service MAY reduce HTML to its visible text, keeping reading order and link targets. The judge MUST see the same representation as the extractor, from the same bytes.
- When no endpoint in either role's group can read any supplied representation, refuse at submit with `415` and the error code `unsupported_document`. The client cannot help a job that fails later for this reason.
- The representation choice is part of the service's configuration and therefore of `service_version` (section 11).

### Idempotency

The client MAY send `Idempotency-Key: <string of up to 128 characters>`. The service MUST then treat a repeat of the request with the same key, from the same application, within 24 hours, as the same job: answer `202` (or `200`) with the existing job's id and `Idempotency-Replayed: true`, and run nothing. The same key with a different body (compare a hash of the parts) is `422` with `idempotency_mismatch`. Keys are scoped per application. TravelInbox derives its key from the document's content hash and its prompt and schema versions, so a worker retry after a lost response never pays for a second extraction.

### Responses

| Status | Meaning | Body |
| --- | --- | --- |
| `202` | Job created. `Location: /v1/jobs/{id}`. | The job resource of section 5, with `status: "queued"`. The client reads only `id` and `status`. |
| `400` | Malformed multipart, a required part missing or repeated, `retention` or `max_attempts` unreadable. | error |
| `413` | Over a limit of the table above. | error |
| `415` | Document media type not accepted, or no configured endpoint can read it (`unsupported_document`). | error |
| `422` | The schema is not a valid JSON Schema 2020-12 (`invalid_schema`), or `idempotency_mismatch`. | error |
| `401`, `403` | Section 3. | error |
| `429` | Rate limited or quota spent. Section 8. `Retry-After` MUST be present. | error |
| `5xx` | Service failure. The client retries later with a new submission (or the same `Idempotency-Key`). | any |

`400`, `413`, `415` and `422` mean the request itself is wrong: TravelInbox does not retry them and marks the document `failed`. Everything else is retried.

## 5. The job resource: `GET /v1/jobs/{id}`

`200` with:

```json
{
  "id": "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB",
  "status": "accepted",
  "result": {"schema_version": 1, "reservations": []},
  "feedback": [
    {"pointer": "/reservations/0/starts_at_local", "message": "…", "source": "judge"}
  ],
  "attempts": 2,
  "service_version": "vde-1.4.0+c0ffee12",
  "error": null,
  "created_at": "2026-10-16T09:12:03Z",
  "finished_at": "2026-10-16T09:12:41Z",
  "expires_at": "2026-10-17T09:12:41Z"
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Opaque, URL-safe, unique per service. |
| `status` | string | `queued`, `running`, then exactly one of the terminal states `accepted`, `rejected`, `failed`. A terminal status never changes. |
| `result` | object or `null` | The last candidate the extractor produced, parsed JSON. `null` while in progress, and when no attempt produced parseable JSON. For `accepted` it is never `null` and validates against the schema. For `rejected` and `failed` it is kept exactly as the extractor produced it, invalid or not, so the caller loses nothing. |
| `feedback` | array | The final feedback on `result`: the judge's on a rejection, the schema validator's when the last candidate did not validate, empty on `accepted` and while in progress. Each entry: `pointer` (RFC 6901 JSON pointer into `result`; `""` for the whole result; `/reservations/-` style pointers for something missing from an array), `message` (text meant for a human), `source` (`judge` or `schema`). |
| `attempts` | integer | Extractor calls made, at least 1 in any terminal state unless `error` says the extractor was never reached. |
| `service_version` | string | Section 11. Also sent as the `X-Service-Version` response header. |
| `error` | object or `null` | `null` unless `status` is `failed`: `{"code": "…", "message": "…"}`, codes in section 9. |
| `created_at`, `finished_at` | timestamp, timestamp or `null` | `finished_at` is set with the terminal status. |
| `expires_at` | timestamp | When the job becomes `404` (section 10). |

State machine: `queued` → `running` → one of `accepted`, `rejected`, `failed`. Only `accepted` is acceptance. It MUST mean all of: the judge answered `accept` on `result`, `result` validates against the schema, and `error` is `null`. TravelInbox treats every other combination, including a status it does not know, as not accepted, keeps `result` and `feedback`, and sends the document to review.

Other responses: `404` when the job does not exist, has expired, or belongs to another application. `401`, `403`, `429`, `5xx` as elsewhere. Polling MUST NOT count against the quota of section 8 and SHOULD have a generous rate limit (TravelInbox polls each job from every 2 s backing off to every 30 s).

`DELETE /v1/jobs/{id}` MAY exist, returning `204` and deleting the document, pages, result and feedback at once. TravelInbox does not call it; `retention: none` covers its need.

## 6. The extraction loop

For one job the service MUST run this loop and nothing else that changes the result:

1. **Validate the request** (section 4) and account for the quota (section 8). Refuse before any model call if either fails.
2. **Build the extractor prompt** from, in this order: the service's own fixed framing for the extractor role (what the task is, that the answer is exactly one JSON object conforming to the schema, no prose, no code fences); the caller's `extractor_instructions`, verbatim and complete; the schema; the document representation; and, on a retry, the previous candidate and the feedback on it. The service MUST NOT shorten, rewrite or translate the caller's instructions, and MUST NOT add domain content.
3. **Call the extractor** through the extractor role's endpoint group, with failover between endpoints as configured. Parse the answer as JSON (the service MAY strip code fences and leading or trailing prose; it MUST NOT repair the JSON beyond that).
4. **Validate the candidate against the schema.** A candidate that does not validate, or that is not JSON at all, goes back to step 2 with `source: "schema"` feedback (one entry per violation, pointer to the offending location, a message naming the keyword and the expected form). This counts as an attempt and does not call the judge.
5. **Build the judge prompt** from: the service's fixed framing for the judge role, which defines the verdict format of section 7; the caller's `judge_instructions`, verbatim; the schema; the same document representation the extractor saw; and the candidate.
6. **Call the judge** through the judge role's endpoint group. Parse the verdict (section 7). A verdict that cannot be parsed is asked for once more in the same attempt; if that fails too, the job is `failed` with `judge_failed`. A judge call that fails on every endpoint of its group is `failed` with `provider_unavailable`. **A judge failure never counts as acceptance.**
7. `accept` ends the job as `accepted` with empty feedback. `reject` goes back to step 2 with the judge's feedback, until the attempt limit; the last rejection ends the job as `rejected`, carrying the last candidate and the last feedback.

Attempt limit: configured per application by the operator, default 3; `max_attempts` in the request can only lower it. Escalation to a stronger model on later attempts, if configured, is invisible to the caller except through `service_version`.

Time limit: every job MUST reach a terminal state within a configured limit that defaults to 5 minutes and MUST NOT exceed 8 minutes, counted from `created_at` and including queue time. On expiry the job is `failed` with `timeout`, keeping any candidate and feedback produced so far. TravelInbox gives up on a job 10 minutes after submitting it; a job the service leaves in `running` past that is lost work for both sides.

Endpoint failover handles availability (connection errors, `5xx`, provider rate limits, timeouts per call), is bounded in tries and total time, and never crosses into the other role's group. Content problems (a rejected candidate) are not failover; they are the loop above.

## 7. The judge verdict

The service's judge framing MUST ask for, and the service MUST parse, exactly this JSON object as the judge's whole answer:

```json
{"verdict": "accept", "feedback": {}}
```

```json
{"verdict": "reject", "feedback": {"/reservations/0/booking_ref": "The source prints LX42QZ (page 1, top right); the candidate has LX42Q2."}}
```

- `verdict` is `accept` or `reject`. Anything else is an unparseable verdict (section 6, step 6).
- `feedback` maps JSON pointers into the candidate to one message each. A `reject` MUST have at least one entry; an `accept` MAY carry entries, which the service forwards as advisory feedback on an accepted job (TravelInbox stores them but does not show them to the user). The service converts the map to the `feedback` array in the order the judge gave, with `source: "judge"`.
- The service MUST NOT edit messages, except to cap their length (the cap SHOULD be at least 2,000 characters per message). Messages are shown to end users by the caller.

The caller's `judge_instructions` describe the criteria; they MAY restate this format, as TravelInbox's do, and the service's framing MUST agree with it. If the service needs the judge to return more (a confidence, a category), it adds optional keys and ignores their absence; the two keys above stay as they are.

## 8. Quota and rate limits

Two separate mechanisms:

- **Quota**: how many jobs an application may submit per window (the operator configures the window, such as a day). Submissions count; polls and quota reads do not. When the quota is spent, `POST /v1/jobs` answers `429` with `X-Quota-Remaining: 0`.
- **Rate limit**: a short-term ceiling on requests per second or minute, and SHOULD include a ceiling on jobs in progress per application. Exceeding it answers `429` with `X-Quota-Remaining` above zero.

Every response to an authenticated request, including `429` and errors, MUST carry:

| Header | Meaning |
| --- | --- |
| `X-Quota-Remaining` | Jobs the application may still submit in the current window. |
| `X-Quota-Reset` | When the window resets, RFC 3339 UTC. |

Every `429` MUST carry `Retry-After` (seconds, or an HTTP date). TravelInbox reads `X-Quota-Remaining: 0` as "stop submitting until `X-Quota-Reset`", holding its extraction jobs without using up their attempts, and reads any other `429` as "wait `Retry-After` and submit again".

`GET /v1/quota` MUST return `200` with the same numbers as a body, without changing them:

```json
{"limit": 2000, "remaining": 1834, "reset": "2026-10-17T00:00:00Z", "window": "P1D"}
```

TravelInbox calls it at worker start to seed its quota gauge and to verify the credential.

## 9. Errors

Every error response has this body, and `Content-Type: application/json`:

```json
{"error": {"code": "quota_exhausted", "message": "The daily quota of 2000 jobs is spent.", "request_id": "r_…"}}
```

HTTP error codes the client understands:

| Status | `error.code` | When |
| --- | --- | --- |
| `400` | `bad_request` | Malformed multipart, missing or repeated part, bad `retention` or `max_attempts`. |
| `401` | `unauthorized` | No token, or an unknown, expired or revoked one. |
| `403` | `forbidden` | Token outside its scope. |
| `404` | `not_found` | Unknown, expired or foreign job. |
| `413` | `payload_too_large` | Section 4 limits. |
| `415` | `unsupported_media_type` | Document media type never accepted. |
| `415` | `unsupported_document` | Accepted type, but no configured endpoint can read this document. |
| `422` | `invalid_schema` | The `schema` part is not a valid JSON Schema 2020-12. |
| `422` | `idempotency_mismatch` | Same `Idempotency-Key`, different request. |
| `429` | `rate_limited` | Rate limit. `X-Quota-Remaining` above zero. |
| `429` | `quota_exhausted` | Quota spent. `X-Quota-Remaining: 0`. |
| `500`, `502`, `503`, `504` | `internal`, `provider_unavailable`, `unavailable`, `timeout` | Service or provider failure at request time. |

Job error codes, in `error.code` of a `failed` job:

| `error.code` | Meaning | `result` |
| --- | --- | --- |
| `provider_unavailable` | Every endpoint of a role's group failed within the failover bounds. | Last candidate, if any. |
| `extractor_failed` | The extractor endpoint answered but with something unusable on every attempt (empty answer, refusal, output that is not JSON after the allowed cleanup). | `null`, or the last parseable candidate. |
| `judge_failed` | The judge's answer could not be parsed twice in a row. | Last candidate. |
| `timeout` | The job's time limit (section 6) expired. | Last candidate, if any. |
| `internal` | Anything else. | Last candidate, if any. |

The service MAY add codes. The client treats any unknown code as a service failure and retries the document later.

## 10. Retention, privacy and logging

- `retention: none` (what TravelInbox always sends): the service MUST delete the document and pages as soon as the job is terminal, and MUST delete the result, feedback and error on the first `GET` that returns a terminal status, or 24 hours after `created_at`, whichever comes first. After that the job is `404`. `expires_at` reports the 24-hour bound. Nothing of the job's content survives in the service's database, object store, queue or cache afterwards; only the audit record of section 12.
- `retention: default`: the operator's configured retention for the application. The operator MUST be able to force `none` for an application regardless of what requests say.
- Document bytes, page images, instructions, schema, candidates, results and feedback MUST NOT appear in logs, traces, metrics or error messages at any level. A debug mode that logs them MUST be off by default and documented as unsafe for production.
- What the AI providers retain is the operator's choice of provider and settings. The service MUST document, per provider adapter, what the adapter sends and which provider retention or training settings it sets, so the operator can approve an endpoint knowingly. Only operator-approved endpoints may receive documents; a request can never name an endpoint or model.
- Transport is HTTPS; stored jobs are encrypted at rest when the deployment's storage supports it.

## 11. Versioning and compatibility

- `service_version` MUST identify the configuration that produced a result well enough that two results with the same string came from the same service release, the same role configuration (endpoint groups, models, escalation, attempt limit) and the same fixed framings. Suggested form: `<service>-<semver>+<8 hex of a hash over the effective role configuration>`, for example `vde-1.4.0+c0ffee12`. It MUST be in the job body and in the `X-Service-Version` header of every `/v1/jobs` response. TravelInbox stores it with every document and reports its evaluation score per version.
- The API base path is the API version. Within `/v1`, changes are additive only (section 2). A breaking change ships as `/v2`, and `/v1` stays available for at least 12 months after.
- Instructions and schema are versioned by the caller, not the service; the service sees them as opaque per-request input.

## 12. Operations

- `GET /healthz`: `200` without authentication when the service can accept jobs (database and queue reachable; at least one endpoint per role configured and not marked down), else `503`. No body needed beyond `{"status": "ok"}`.
- `GET /metrics`: Prometheus text format, SHOULD exist, protected by network or a separate token. Useful series: jobs by terminal status, attempts per job, extractor and judge latency per endpoint, failovers, quota remaining per application.
- Audit log: token creation, rotation and revocation, and per job: application, job id, `created_at`, document media type and size, page count, terminal status, attempts, `service_version`, error code. Never content.
- Configuration: one YAML file plus environment variables for secrets, following the shape of the project brief (`endpoints`, `groups`, `roles`, `max_attempts`, plus `applications` with their quota, rate limit, attempt limit and retention). `vde config check` MUST validate it without network calls beyond an optional endpoint probe: every group references defined endpoints, every endpoint-model pair declares the document media types it accepts, and both roles have a group.
- Packaging: one container image on a public registry with the server as entrypoint, tagged by semver, and the same binary offering `serve`, `token` and `config check`. TravelInbox runs it from docker compose in development and on the same VM as its own services in production, so the image MUST run on `linux/arm64` as well as `linux/amd64`.
- A minimal development configuration MUST be documented: one provider, one model for both roles, one application and token, so a consumer can run the service with a single provider key in an environment variable.

## 13. How TravelInbox calls the service

TravelInbox uses only `POST /v1/jobs`, `GET /v1/jobs/{id}` and `GET /v1/quota`, always with `retention: none`, always from its worker, with `EXTRACTION_URL` and `EXTRACTION_TOKEN`. The client is `backend/internal/extract/service.go`; nothing outside it depends on the wire format. As of this version the client sends neither `Idempotency-Key` nor the quota read at start; both come with `roadmap/v0.1-core/05-extraction/05-contract-alignment`, and a service that ships before then loses nothing by implementing them.

| Situation | Client behaviour | Document state |
| --- | --- | --- |
| `202`, then poll | Polls from 2 s, doubling to 30 s, until a terminal status or 10 minutes after submitting. | `processing` |
| Terminal `accepted` | Validates `result` against the schema itself and stores one reservation per item. | `done` |
| `rejected`, `failed`, `accepted` with an `error`, unknown status | Keeps `result` and `feedback`, stores the schema-valid items flagged for review, shows the feedback to the user. | `needs_review` |
| Still `queued` or `running` after 10 minutes | Timeout; the extraction job retries with a new submission (same `Idempotency-Key`). | `processing` |
| `429` with `X-Quota-Remaining: 0`, or a known zero quota before submitting | No submission; the job snoozes until `X-Quota-Reset` without using an attempt. | `processing` |
| `429` with quota left | Waits `Retry-After` and resubmits, up to 5 times within the 10 minutes. | `processing` |
| `400`, `413`, `415`, `422` | Not retried. | `failed`, error class `invalid` |
| `401`, `403`, `5xx`, transport error, malformed body, `404` on poll | Retried with backoff (30 s, 1, 2, 4 min); a poll failing with `429`, `5xx` or a transport error is repeated, giving up on the third failure in a row. | `processing`, then `failed` with error class `service` after the fifth attempt |

`X-Quota-Remaining` feeds the `extraction_quota_remaining` metric. `service_version`, `attempts`, `result`, `feedback` and `error` are stored in `documents.raw_extraction` together with TravelInbox's own `prompt_version` and `schema_version`.

TravelInbox's travel inputs, for reference and for the service's own tests: `backend/internal/extract/prompts/extractor.md`, `backend/internal/extract/prompts/judge.md`, `shared/extraction-schema.json`.

## 14. Conformance

What TravelInbox provides to the service's developers:

- This document and `shared/extraction-service-api.yaml`, which the service's own OpenAPI document SHOULD be a superset of.
- A reference stub of the service in `backend/internal/extract/service_test.go` (`newStub`), which is what the client is tested against. It answers from the evaluation corpus and shows the exact multipart layout, headers and bodies the client sends and reads.
- A redacted evaluation corpus, `backend/testdata/corpus/` (51 travel documents with ground truth as of this version), usable as a test set for the service's own quality work, under this repository's licence.
- A live check the service's developers can run against any deployment, from `backend/`:

```bash
EXTRACTION_URL=https://extract.example.com EXTRACTION_TOKEN=… go test ./internal/extract -run TestServiceLive -count=1
```

```bash
EXTRACTION_URL=https://extract.example.com EXTRACTION_TOKEN=… make eval EXTRACTOR=service
```

Acceptance checklist for the service's first release, from TravelInbox's side:

1. Every MUST in sections 2 to 12 holds; the live test above passes.
2. A request without a token, with a revoked token, and with another application's token gets `401`, `401` and (for a job) `404`, and the service's provider usage counter does not move.
3. A text document, a PDF with pages, a JPEG and an HTML email body each reach a terminal state within the time limit with `service_version` and `attempts` set.
4. A candidate that violates the schema is sent back to the extractor with `source: "schema"` feedback, without a judge call; after the attempt limit the job is `rejected` with the invalid candidate kept in `result`.
5. With the judge endpoint group unreachable, the job ends `failed` with `provider_unavailable` and never `accepted`.
6. With the primary extractor endpoint unreachable, jobs complete through the fallback endpoint, and the only visible difference is latency.
7. A spent quota answers `429` with `X-Quota-Remaining: 0`, `X-Quota-Reset` and `Retry-After`; polls still work.
8. With `retention: none`, the document and pages are gone at the terminal state and the job is `404` after its terminal state has been read.
9. A repeated submission with the same `Idempotency-Key` returns the same job and runs no model call.
10. `make eval EXTRACTOR=service` reports a no-correction share for flights and lodging, whatever the number; the targets in `docs/PRODUCT.md` are TravelInbox's prompt problem, not an acceptance criterion for the service.

## 15. Examples

Submit a pasted text document (TravelInbox sends a `page` part per rendered page for PDFs and none here):

```bash
curl -sS https://extract.example.com/v1/jobs \
  -H "Authorization: Bearer $EXTRACTION_TOKEN" \
  -H "Idempotency-Key: 3f9c…" \
  -F "document=@booking.txt;type=text/plain; charset=utf-8" \
  -F "extractor_instructions=<extractor.md;type=text/markdown" \
  -F "judge_instructions=<judge.md;type=text/markdown" \
  -F "schema=<extraction-schema.json;type=application/schema+json" \
  -F "retention=none"
```

```json
{"id": "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", "status": "queued", "result": null, "feedback": [], "attempts": 0, "service_version": "vde-1.4.0+c0ffee12", "error": null, "created_at": "2026-10-16T09:12:03Z", "finished_at": null, "expires_at": "2026-10-17T09:12:03Z"}
```

Poll:

```bash
curl -sS https://extract.example.com/v1/jobs/j_01JA3V9Q6ZK7M4W8X2R5T1N0HB -H "Authorization: Bearer $EXTRACTION_TOKEN"
```

A rejected job after three attempts:

```json
{
  "id": "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB",
  "status": "rejected",
  "result": {"schema_version": 1, "reservations": [{"kind": "ferry", "title": "Lisbon to Barreiro", "booking_ref": "LX42Q2", "starts_at_local": "2027-05-12T09:15:00", "confidence": {"kind": 1, "title": 0.9, "booking_ref": 0.8, "starts_at_local": 0.95}}]},
  "feedback": [{"pointer": "/reservations/0/booking_ref", "message": "The source prints LX42QZ; the candidate has LX42Q2.", "source": "judge"}],
  "attempts": 3,
  "service_version": "vde-1.4.0+c0ffee12",
  "error": null,
  "created_at": "2026-10-16T09:12:03Z",
  "finished_at": "2026-10-16T09:13:30Z",
  "expires_at": "2026-10-17T09:12:03Z"
}
```

## 16. Open points and change process

Points left to the service's developers, with TravelInbox's preference:

- A completion webhook and a synchronous `POST /v1/extract` for small documents MAY exist; TravelInbox uses neither (polling needs no inbound endpoint and no second shared secret).
- Whether HTML is given to the model as HTML or as visible text (section 4). TravelInbox has no preference as long as the choice is in `service_version`.
- Finer token scopes, a `DELETE /v1/jobs/{id}`, extra judge keys (section 7) and extra job fields are welcome and need no change on this side.

Changing this contract: until the service repository exists, changes are made here first, as a new version of this document plus `shared/extraction-service-api.yaml`, with the reason recorded in `docs/adr/`. Once the service publishes its own API document, that becomes the source and this file pins the version TravelInbox is written against, listing any deviation. Either way, `backend/internal/extract/service.go` and its stub change in the same pull request as the contract.

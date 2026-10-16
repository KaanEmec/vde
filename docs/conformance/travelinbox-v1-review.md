# TravelInbox contract v1: review and feedback

Status: draft for the review with TravelInbox, written 2026-10-16 while v0.2 is being
built. Inputs: `FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md` (version 1, sections 3, 10
and 16 especially), `FromTravelApp/extraction-service-api.yaml`, the service's
[`api/openapi.yaml`](../../api/openapi.yaml) and the code.

Every statement carries one of two marks, checked against the code on the date above:

- **As implemented**: the behavior is in the tree and covered by tests.
- **Planned**: a v0.2 roadmap job that is not finished; the behavior is the intended one
  and is not yet in effect.

Nothing here changes `/v1` semantics; a change to them would be `/v2`.

## 1. Decisions on the open points (contract section 16)

The owner of both services decided these on 2026-10-16
([open questions](../roadmap/open-questions.md), numbers in brackets).

| Open point | Decision | Mark |
| --- | --- | --- |
| Completion webhook and synchronous `POST /v1/extract` | Neither in v0.2. A synchronous endpoint and webhooks are v0.3 options ([ADR 0005](../architecture/decisions/0005-contract-first.md)); TravelInbox uses neither. | Not built |
| HTML representation | Raw HTML, sent to the model as text. A per-application option `documents.html: visible_text` reduces it to readable text in reading order with link targets. The policy is part of `service_version` (13, [ADR 0006](../architecture/decisions/0006-representation-policy.md)). | As implemented (policy, conversion and `service_version` input); intake of HTML over HTTP is planned |
| Finer token scopes | One finer scope, `jobs:read` (poll and quota only), next to `jobs` (submit, poll, delete, quota). Unknown scopes are refused when a token is created. | As implemented |
| `DELETE /v1/jobs/{id}` | Implemented in v0.2 (15). Deletes the job's content at once; afterwards `404`. Requires the `jobs` scope. | Route and API document present; handler planned (answers `503 unavailable` today) |
| Extra judge keys | One: optional `confidence`, a number from 0 to 1 beside `verdict` and `feedback`. Absent or invalid means none (14). | As implemented |
| Extra job fields | `judge_confidence` and, for emails, `attachments`; see the list below. | Engine, store and API document as implemented; exposure on the job body is planned with the GET handler |
| Default per-application limits | 300 requests per minute, 30 submissions per minute, 20 jobs in progress, quota 1000 per `P1D`; all configurable (16). | Configuration and defaults as implemented; enforcement planned |

## 2. Superset of the contract

Everything below is additive. A client written only against contract v1 never needs to
send or read any of it.

| Addition | What it is | Mark |
| --- | --- | --- |
| Optional `schema` | Without a `schema` part the job runs schema-less: no schema feedback, the judge alone decides acceptance, and `result` is any JSON object. TravelInbox always sends a schema. | Loop as implemented; the submit handler is planned |
| `DELETE /v1/jobs/{id}` | See above. | Handler planned |
| `X-Request-Id` | Echoed when the client sends 1 to 128 visible ASCII characters, else generated as `r_<ULID>`; on every response; also in the error body as `request_id`. | As implemented |
| `GET /v1/openapi.yaml` | Serves the service's API document to any valid token (any scope). | As implemented |
| `jobs:read` scope | See above. | As implemented |
| `judge_confidence` | Job field, number 0 to 1 or `null`; advisory only. | See ambiguity 3 |
| `attachments` | For a `message/rfc822` document, the email's attachments as metadata; their content is not read. Absent otherwise. | Parsing as implemented; field on the job planned |
| `message/rfc822` | Document media type: headers that carry meaning plus the best body become the text representation. Callers submit attachments as separate jobs. ADR 0005 first scheduled this for v0.3; decision 7 and ADR 0006 moved it to v0.1. | Parsing as implemented; intake planned |
| `text/markdown`, `image/webp`, `image/heic` | The contract's SHOULD types, accepted. | As implemented in the media table |
| Raw HTML by default | See section 1. | As implemented |
| No server-side rendering | The service never renders PDF pages. A PDF that no configured endpoint can read and that comes without `page` parts is refused with `415 unsupported_document`. Operators configure a PDF-capable model, or callers send pages as the contract allows. | As implemented in representation selection; the refusal at submit is planned |
| `method_not_allowed` | A known route with the wrong method answers `405 method_not_allowed` with `Allow`. An added error code, as section 9 allows. | As implemented |
| `X-Service-Version` per application | The header and the job's `service_version` use the hash of the application's own effective configuration (attempt limit, HTML policy). | As implemented |
| `insecure_http` development switch | `server.insecure_http: true` accepts plain HTTP from any peer. Off by default; `vde serve` and `vde config check` warn; refused together with `server.tls`. It exists for containers and development on a trusted private network. It is the one setting that relaxes a contract MUST (HTTPS outside loopback), so a TravelInbox deployment must not use it. | As implemented |

Deviations from the contract: none known. Where the contract says MAY or SHOULD, section
1 and the table above say what the service chose.

## 3. Default limits

| Limit | Default | Configuration key | Mark |
| --- | --- | --- | --- |
| Document size | 25 MiB | `limits.document_bytes` | Value as implemented; enforcement planned |
| Page image size | 10 MiB | `limits.page_bytes` | Same |
| Pages per job | 20 | `limits.pages` | Same |
| Requests per minute, per application | 300 | `applications[].rate.requests_per_minute` | Same |
| Submissions per minute, per application | 30 | `applications[].rate.submits_per_minute` | Same |
| Jobs in progress, per application | 20 | `applications[].rate.max_in_progress` | Same |
| Quota | 1000 submissions per `P1D` | `applications[].quota.limit`, `quota.window` | Same |
| Attempt limit | 3; a request can only lower it | `limits.max_attempts`, `applications[].max_attempts` | As implemented |
| Job deadline | 5 minutes, at most 8 | `limits.job_timeout` | As implemented |
| Retention for `default` | 24 hours | `applications[].retention.default` | Value as implemented; deletion planned |
| Token lifetime cap | 730 days | `auth.max_token_ttl` | As implemented |

The contract's request limits (64 KiB instructions, 256 KiB schema, 128 MiB body) are
fixed by the submit handler, which is planned. TravelInbox polls every 2 s backing off to
30 s; with 20 jobs in flight that stays under the request limit once the backoff starts.

## 4. Ambiguities found during implementation

How the service reads points the contract leaves open. None needs a contract change; each
is a statement TravelInbox can rely on.

1. **`attempts` counts an unparseable extractor answer.** Yes. `attempts` is the number of
   extractor calls (section 5), so an answer that is not JSON, a refusal and a candidate
   that fails the schema each count, as does a call that fails permanently. An unparseable
   judge verdict does not: the re-ask happens within the same attempt. As implemented.
2. **A cancelled context ends as `timeout`.** When the job's deadline passes or its
   context is cancelled, the loop ends the job `failed` with `timeout`, even if a provider
   call was the cause, and keeps the last parseable candidate in `result`. As implemented.
3. **Judge confidence never affects acceptance.** Only `verdict` decides. `judge_confidence`
   is the confidence of the last parsed verdict, `null` when the judge gave none or a
   value outside 0 to 1 (which is ignored and logged). A confidence on a `reject` is kept
   too. As implemented in the engine; the field is exposed on the job when the GET
   handler lands (planned).
4. **HTTPS enforcement answers `400 bad_request`.** The contract requires HTTPS outside
   loopback but names no status. A plain-HTTP request from a non-loopback peer, or behind
   a trusted proxy one without `X-Forwarded-Proto: https`, gets `400` with the fixed
   message "HTTPS is required." As implemented.
5. **`method_not_allowed`.** Added code for `405`; an unknown route is `404 not_found`
   (section 9 lists no code for either). As implemented.
6. **`unavailable` (`503`) while handlers are not wired.** Until the submit, poll, delete
   and quota handlers land, those routes answer `503 unavailable` after authentication, and
   `/metrics` the same. This is a development-state behavior and disappears when the
   epics complete; the v0.2.0 release must not contain it. As implemented today.
7. **Authentication failures are indistinguishable.** Missing, malformed, unknown, expired,
   revoked and wrong-secret tokens, and tokens of a disabled application, all answer the
   same `401` body with `WWW-Authenticate: Bearer realm="vde"`. The reason goes only to the
   audit record. A valid token with too narrow a scope answers `403`. As implemented.
8. **Accepted jobs may carry advisory feedback.** Section 7 allows it; the service forwards
   it, and a `reject` always has at least the judge's entries. As implemented.
9. **`service_version` is per application.** The string is `vde-<semver>+<8 hex>`; the hash
   covers the effective role configuration, framings, HTML policy and attempt limit of
   that application, so two applications on one deployment can differ. A build without a
   release version reports `vde-0.0.0-dev+<hex>`. As implemented.

## 5. Proposed pinned version

| Item | Value |
| --- | --- |
| API document version | `0.2.0`, the `info.version` of `api/openapi.yaml` |
| Document link to pin | `https://github.com/kaanemec/vde/blob/v0.2.0/api/openapi.yaml` (planned: the tag `v0.2.0` does not exist until the release job runs) |
| `service_version` prefix | `vde-0.2.0`; the full value is `vde-0.2.0+<8 hex>`, where the suffix changes with configuration and is not part of the pin |
| Contract | TravelInbox's file keeps the prose contract version 1 and records `vde-0.2.0` as the version it is written against, as section 16 describes. |

What TravelInbox needs, without reading the service's code:

1. Replace `shared/extraction-service-api.yaml` with `api/openapi.yaml` at the tag (a
   superset: every path, method, response code and required field of the old file is
   present, which a service test checks), or keep the old file and pin the tag.
2. Update the stub in `backend/internal/extract/service_test.go` for the additions it
   chooses to use: `judge_confidence`, `attachments`, `method_not_allowed` and the
   `405` behavior, and `X-Request-Id` on every response. Ignoring all of them is valid.
3. List `insecure_http` as a service setting that must be off in staging and production.
4. Send `retention: none` as today; nothing else changes on the client.

## 6. Open items for the review meeting

- Confirm the decisions in section 1 and the readings in section 4.
- Notes and resulting changes go into this file; an architectural consequence becomes an
  ADR. (Planned: the meeting has not happened.)
- Re-check every "planned" mark against the release candidate and remove the marks; the
  acceptance checklist tests (epic 09, job 01) are the evidence.

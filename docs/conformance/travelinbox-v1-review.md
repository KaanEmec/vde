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
| PDF preparation in the service (v0.4, amends "no server-side rendering") | Until v0.3 the service never rendered PDF pages. Since v0.4 ([ADR 0010](../architecture/decisions/0010-pdf-preprocessing-in-service.md)), when no model of a role reads `application/pdf` and the caller sent no `page` parts, the default `documents.pdf.mode: auto` sends the PDF's text layer (every page at least 200 characters) or its rendered pages. Only `documents.pdf.mode: native` keeps the v0.3 behaviour of refusing such a PDF with `415 unsupported_document` (contract 4.11). A PDF-capable model still gets the PDF as given, and caller pages still win. See section 6. | As implemented (`TestPDFPreparedByService`, `TestSubmitPDFPreparedByService`, `TestServeJudgePolicies`; the conformance suite's instance without a PDF reader runs `mode: native`) |
| `content_deleted_at` | Job field, RFC 3339 or `null`; present only in an idempotent replay of a job whose content is gone (read once under `retention: none`, or expired). Absent on every pollable job. | As implemented; see ambiguity 10 |
| `method_not_allowed` | A known route with the wrong method answers `405 method_not_allowed` with `Allow`. An added error code, as section 9 allows. | As implemented |
| `X-Service-Version` per application | The header and the job's `service_version` use the hash of the application's own effective configuration (attempt limit, HTML policy). | As implemented |
| `insecure_http` development switch | `server.insecure_http: true` accepts plain HTTP from any peer. Off by default; `vde serve` and `vde config check` warn; refused together with `server.tls`. It exists for containers and development on a trusted private network. It is the one setting that relaxes a contract MUST (HTTPS outside loopback), so a TravelInbox deployment must not use it. | As implemented |
| `judge` request part (v0.4) | Optional part (and JSON field) choosing the judge policy `always`, `never` or `auto` for one job ([ADR 0009](../architecture/decisions/0009-judge-policy-and-grounding.md)). Refused with `400 bad_request` unless the operator set `judge.allow_override` for the application, which TravelInbox's applications do not. Part of the idempotency body hash. | As implemented (part, validation, stored policy, per-policy `service_version`, and the loop behind `never` and `auto`: `TestServeJudgePolicies`) |
| `verification` (v0.4) | Job field: `judge`, `grounded` or `none` on terminal jobs, `null` while queued or running. Under the default `always`, `accepted` pairs with `judge` and every other status with `none`, so TravelInbox sees `judge` on its accepted jobs. Rows finished before v0.4 read the same way. | As implemented |
| `extracted` status (v0.4) | Terminal status of a schema-valid final candidate under judge policy `never`: never acceptance. Contract section 5 has clients treat an unknown status as not accepted, so it is additive; TravelInbox's applications run `always` and never see it. | As implemented (`TestServeJudgePolicies`, `TestExtractSyncExtracted`, `TestExtractedStatusPassesThrough`) |
| `source: "grounding"` feedback entries (v0.4) | Feedback entries of the deterministic grounding check, in the job's `feedback` only under `never` and `auto`. Under `always` they reach only the extractor, so a contract-v1 client sees only `judge` and `schema`. | As implemented (scenarios in `testdata/scenarios/judge-policy`; under `always` none reach the job's `feedback`, also while running or when the sweeper times a job out) |
| Grounding repair under `always` (v0.4) | Under the default policy a candidate with critical values the document does not print goes back to the extractor once, with grounding feedback, before the judge is asked (at most one extra extractor call per job, only while an attempt remains). No new status, field or feedback source reaches the client; the change is visible only through `service_version`, which hashes the grounding rules (`ground-1`). | As implemented (`always-repair-*` scenarios; the conformance suite passes unchanged under `always`) |

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
10. **A replay of a job whose content is gone. Open point for TravelInbox.** Section 4
    requires a repeat within 24 hours to be the same job and run nothing; section 5 says
    an `accepted` job's `result` is never `null`; section 10 deletes the content of a
    `retention: none` job at its first terminal read. A replay after that read cannot
    satisfy all three with the old body. Chosen behaviour: `202`,
    `Idempotency-Replayed: true`, the same job id, `status` kept as it was, `result` and
    `error` `null`, `feedback` `[]`, and the added field `content_deleted_at` set (RFC
    3339). No provider call, no quota charge. The same answer is given for a
    `retention: default` job replayed after its `expires_at` (its row is kept, metadata
    only, until `created_at` + 24 hours so that the key keeps naming the job). The
    client reads `content_deleted_at` as "already delivered, do not re-submit". A poll of
    such a job stays `404`. TravelInbox always polls a replayed job and then gets `404`,
    so it loses nothing; a client that trusts the replay body without reading
    `content_deleted_at` would see `accepted` with `result: null`. To confirm with the
    contract owner: this reading of §5 for the replay body (alternatives considered:
    `404 not_found` for the replay, which breaks the §4 "same job" reading). As
    implemented (defect D2 of the conformance suite).

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
- Superset statuses and fields (v0.4): TravelInbox keeps `always`; confirm the client
  tolerates `verification` (present on every terminal job, `judge` on its accepted jobs)
  and would treat `extracted` as not accepted and an unknown feedback `source` like
  `judge`. All are opt-in supersets: `extracted`, `verification: grounded` and
  `source: "grounding"` appear only for applications an operator moves to `never` or
  `auto`, and the `judge` part only where `judge.allow_override` is set. Note also that the
  grounding repair under `always` can add one extractor call to a job (`attempts` counts
  it), and that its `service_version` changed with v0.4.
- PDF preparation (v0.4, ADR 0010): with the default `documents.pdf.mode: auto`, a PDF
  without pages for models that read no `application/pdf` is no longer refused (contract
  4.11) but sent as its text layer or rendered pages. A deployment that wants 4.11's `415`
  sets `documents.pdf.mode: native`; the conformance suite's instance without a PDF reader
  runs that mode, so contract 4.11 stays tested exactly. Confirm which TravelInbox wants;
  when its models read PDFs natively, the mode changes nothing for it. Proposal: keep
  `auto` (a PDF that would have been refused is now processed, a strict improvement for a
  client that treats `415` as "send pages") unless TravelInbox depends on the refusal.

# TravelInbox contract v1: conformance and MUST traceability

The contract is `FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md` (version 1, 2026-10-16) with
its machine-readable form `FromTravelApp/extraction-service-api.yaml`. Its acceptance
checklist (section 14) and every MUST of sections 2 to 12 are checked by the black-box
suite in [`test/conformance/`](../../test/conformance/doc.go):

```
make conformance                      # go test ./test/conformance/ -count=1
```

## How the suite runs

**Local mode** (the default, and the `conformance` CI job). `TestMain` builds `vde` into a
temporary directory, starts an in-process fake OpenAI-compatible chat completions server,
writes a `vde.yaml` with two endpoints per role (`primary` and `fallback` for the
extractor, `judge` and `judge-fallback` for the judge, all `openai_compatible` pointing
at the fake), runs `vde serve` on a free loopback port with a temporary data directory,
and creates a `jobs` token per application with `vde token create`. Everything after that
is HTTP to the service, so the real adapter, router, worker, store and sweeper are
exercised; the fake's per-test scripts decide what the models answer and its call record
is the provider usage counter. Tests that need another configuration (an unreachable
judge group, an unreachable primary extractor, PDF not accepted, a 3 s job limit, a
trusted proxy, a changed attempt limit) start their own `vde serve` the same way.

The applications of the local configuration:

| Application | Purpose |
| --- | --- |
| `travelinbox-dev` | The application under test: generous quota and rate, retention `default` of 48 h (distinct from the 24 h of `none`). |
| `travelinbox-other` | Another application: ownership (404) and per-application idempotency keys. |
| `travelinbox-quota` | Quota 3 per day, spent by item 7. A separate application so that the suite is independent of test order (`-shuffle`). |
| `travelinbox-rate` | 2 requests per minute, to see `rate_limited`. |
| `travelinbox-forced` | `retention.force_none: true`. |

**Deployment mode.** With `VDE_CONFORMANCE_URL` (base URL, without `/v1`) and
`VDE_CONFORMANCE_TOKEN` (a `jobs` token) set, no binary is built and no server started; the
tests that need only HTTP run against that deployment and the others skip with the reason.
`VDE_CONFORMANCE_FOREIGN_TOKEN` (a token of another application) enables the
foreign-token checks. Jobs there are real extractions and cost provider calls.

```
VDE_CONFORMANCE_URL=https://extract.example.com VDE_CONFORMANCE_TOKEN=... \
  go test ./test/conformance/ -count=1 -v
```

Runs in deployment mode: items 1, 2 (no token, malformed and foreign; not revocation, not
the provider counter), 3, 7 (polls do not charge), 8 (404 after the first terminal read;
not the blob check), 9 (same job and `Idempotency-Replayed`; not the provider counter), and
`TestJobBodyMatchesOpenAPI`, `TestS02RequestID`, `TestS02TimestampsRFC3339UTC`,
`TestS02ErrorMessagesQuoteNoContent` (refusals), `TestS03TokenRefusals` (not expiry),
`TestS03ForeignJobIs404`, `TestS04SubmitResponse`, `TestS04MalformedRequests400`,
`TestS04MediaTypes`, `TestS04LimitsExceeded413` (not the 128 MiB body),
`TestS04IdempotencyMismatch`, `TestS04IdempotencyReplayAfterFirstRead`,
`TestS04IdempotencyKeyScopedPerApplication`, `TestS05TerminalStatusStable`,
`TestS05NotFound`, `TestS05PollingNotCharged`, `TestS08QuotaHeadersOnEveryAuthenticatedResponse`
(not the 429), `TestS08QuotaRead`, `TestS09ErrorCodes` (the codes a client can provoke),
`TestS10DefaultRetention` (not the configured duration), `TestS10RequestCannotNameEndpoint`,
`TestS12Healthz`.

Container mode, the same suite against `docker compose up` on the release branch, is not
wired yet: Docker on the development machine lacks buildx (see the job file).

Every response the suite receives also goes through `checkResponse`
(`test/conformance/client_test.go`), which applies the rules for all responses: the
`X-Request-Id` echo, `X-Service-Version` on `/v1/jobs`, the quota headers on authenticated
responses, the error shape and `Content-Type`, the status of each error code, `Retry-After`
on 429, and the Job, Error and Quota schemas of the TravelInbox API document. Rows that say
"checkResponse" mean every request of the suite checks them.

## Checklist (contract section 14)

| Item | Test |
| --- | --- |
| 1. Every MUST of sections 2 to 12 holds | The table below; `TestItem01TraceabilityComplete` checks that every row names a test of the package or says how it is verified otherwise. TravelInbox's own live test (`TestServiceLive`) runs from their repository. |
| 2. No token, revoked token, another application's token: 401, 401, 404; provider counter unchanged | `TestItem02Unauthenticated` |
| 3. Text, PDF with pages, JPEG, HTML email body each reach a terminal state with `service_version` and `attempts` | `TestItem03DocumentKinds` (also PNG and a whole `.eml`) |
| 4. Schema violation: `source: "schema"` feedback, no judge call, `rejected` with the invalid candidate kept | `TestItem04SchemaViolation` |
| 5. Judge group unreachable: `failed` with `provider_unavailable`, never `accepted` | `TestItem05JudgeGroupUnreachable` |
| 6. Primary extractor unreachable: jobs complete through the fallback | `TestItem06PrimaryExtractorUnreachable` |
| 7. Spent quota: 429 with `X-Quota-Remaining: 0`, `X-Quota-Reset`, `Retry-After`; polls still work | `TestItem07QuotaSpent` |
| 8. `retention: none`: document and pages gone at the terminal state, 404 after the terminal read | `TestItem08RetentionNone` |
| 9. Repeated `Idempotency-Key`: same job, no model call | `TestItem09Idempotency` |
| 10. `make eval EXTRACTOR=service` | Not here: job 02 of epic 09 (`02-travelinbox-live-test.md`). |

## MUST traceability (sections 2 to 12)

Each row is one MUST sentence (or a required behavior stated as fact in a table the
contract makes binding), abridged; the number is section.sequence. "not testable
automatically, verified by: …" marks what this suite cannot reach from outside the
service, with what covers it instead. The defects the suite found (D1 to D3) are fixed and
their tests no longer skip; their history and resolution are in the
[job file](../roadmap/v0.2-service/09-contract-conformance/01-acceptance-checklist-tests.md#defects-found).

| # | MUST (abridged) | Evidence |
| --- | --- | --- |
| 2.1 | Timestamps are RFC 3339 in UTC. | `TestS02TimestampsRFC3339UTC`; checkResponse on every job and quota response |
| 2.2 | HTTPS MUST be required for every request outside loopback. | `TestS02HTTPSRequiredOutsideLoopback` (behind a trusted proxy); a direct non-loopback peer: not testable automatically, verified by: `internal/api` TestRequireHTTPS |
| 2.3 | Within `/v1` the service MUST NOT remove a field or change a field's type or meaning, or a status or error code's meaning. | `TestJobBodyMatchesOpenAPI` and checkResponse (Job, Error, Quota schemas of the TravelInbox document on every response); across releases: not testable automatically, verified by: `internal/api` TestOpenAPISupersetOfTravelInbox on every build |
| 2.4 | Error messages MUST NOT quote document contents, instructions or extraction output. | `TestS02ErrorMessagesQuoteNoContent`, `TestS10NoContentInLogsOrMetrics` |
| 3.1 | Every request to `/v1/*` MUST carry `Authorization: Bearer <token>`. | `TestS03TokenRefusals`, `TestItem02Unauthenticated` |
| 3.2 | A missing, malformed, expired or revoked token MUST get 401 before any provider call, any storage of the upload and any quota accounting. | `TestItem02Unauthenticated` (provider counter, blob store, quota), `TestS03TokenRefusals` (malformed, unknown, expired) |
| 3.3 | A valid token used outside its scope MUST get 403. | `TestS03ScopeForbidden` |
| 3.4 | The service MUST store only a hash of each token. | `TestS03TokenStoredHashed` (no file of the data directory holds the token) |
| 3.5 | Tokens are issued and revoked by the operator, not through the application's API; each has an expiry; several are active per application; revocation takes effect at once. | `TestS03TokenLifecycle`, `TestItem02Unauthenticated` |
| 3.6 | A job is visible only to its application; another application's token gets 404, not 403. | `TestS03ForeignJobIs404`, `TestItem02Unauthenticated` |
| 4.1 | Parts: exactly one `document`, `extractor_instructions`, `judge_instructions`; at most one `schema`, `retention`, `max_attempts`; `retention` is `none` or `default`; `max_attempts` a positive integer; otherwise 400. | `TestS04MalformedRequests400` |
| 4.2 | The result MUST conform to the `schema` part. | `TestItem04SchemaViolation`, `TestS05AcceptedOnlyOnJudgeAccept` |
| 4.3 | `max_attempts` lowers the attempt limit; a higher value is clamped, not refused. | `TestS06AttemptLimit` |
| 4.4 | Document media types the service MUST accept: `application/pdf`, `image/jpeg`, `image/png`, `text/html`, `text/plain`. | `TestItem03DocumentKinds`, `TestS04MediaTypes` |
| 4.5 | Any other document media type is 415. | `TestS04MediaTypes` |
| 4.6 | Limits the service MUST allow: document 25 MiB, page 10 MiB, 20 pages, instructions 64 KiB, schema 256 KiB, body 128 MiB. | `TestS04LimitsAllowed` (all at once, 125.7 MiB) |
| 4.7 | Over any limit: 413. | `TestS04LimitsExceeded413` |
| 4.8 | The service MUST read the whole request before answering, so the client sees the status rather than a reset connection. | `TestS04LimitsExceeded413` (each case sends the whole body and must read a status) |
| 4.9 | The service MUST decide the representation from its configuration: the document natively when an extractor endpoint accepts its type, else the page images; text and HTML as text. | `TestS04RepresentationNativeOrPages`, `TestItem03DocumentKinds` |
| 4.10 | The judge MUST see the same representation as the extractor, from the same bytes. | `TestItem03DocumentKinds` (every kind), `TestS04RepresentationNativeOrPages` |
| 4.11 | When no endpoint of either role can read any supplied representation: 415 `unsupported_document` at submit. | `TestS04UnsupportedDocument`, `TestS04RepresentationNativeOrPages` |
| 4.12 | The representation choice is part of `service_version`. | `TestS11ServiceVersionTracksConfiguration` (models accepting other types give another version) |
| 4.13 | A repeat with the same `Idempotency-Key` from the same application within 24 hours MUST be the same job: 202 (or 200), existing id, `Idempotency-Replayed: true`, nothing run. | `TestItem09Idempotency`, `TestS04IdempotencyReplayAfterFirstRead` (after the first read of a `retention: none` job: the content-deleted form, D2 fixed), `TestS04IdempotencyWindowOutlivesShortRetention` (with a retention below 24 hours, D3 fixed); the full 24 hours: not testable automatically, verified by: `internal/api` TestSubmitIdempotency |
| 4.14 | The same key with a different body is 422 `idempotency_mismatch`. | `TestS04IdempotencyMismatch` |
| 4.15 | Keys are scoped per application. | `TestS04IdempotencyKeyScopedPerApplication` |
| 4.16 | 202 carries `Location: /v1/jobs/{id}` and the job with `status: "queued"`. | `TestS04SubmitResponse`; every submission of the suite (`submitOK`) |
| 4.17 | Every 429 MUST carry `Retry-After`. | `TestItem07QuotaSpent`, `TestS08RateLimited`; checkResponse |
| 5.1 | `GET /v1/jobs/{id}` answers 200 with the job resource: its fields and types. | `TestJobBodyMatchesOpenAPI`; checkResponse on every job body |
| 5.2 | Status runs `queued`, `running`, then exactly one terminal state; a terminal status never changes. | `TestS05TerminalStatusStable` |
| 5.3 | `result` is null while in progress and when no attempt produced parseable JSON. | `TestS04SubmitResponse`, `TestS06NoJSONRepair` |
| 5.4 | For `accepted`, `result` is never null and validates against the schema. | `TestS05AcceptedOnlyOnJudgeAccept`, `TestItem03DocumentKinds`, `TestS04IdempotencyReplayAfterFirstRead` (a replay of a content-deleted job answers `result: null` with `content_deleted_at` set: the reading of §5 recorded as ambiguity 10 of [the review](travelinbox-v1-review.md), D2 fixed) |
| 5.5 | For `rejected` and `failed`, `result` is kept exactly as the extractor produced it, invalid or not. | `TestItem04SchemaViolation`, `TestS06JudgeUnparseableTwice`, `TestItem05JudgeGroupUnreachable`, `TestS06JobTimeout` |
| 5.6 | `feedback`: the judge's on a rejection, the schema validator's when the last candidate did not validate, empty on `accepted` and in progress; entries carry `pointer`, `message`, `source`. | `TestItem04SchemaViolation`, `TestS06RejectUntilAttemptLimit`, `TestS05AcceptedOnlyOnJudgeAccept`, `TestS07FeedbackOrderAndMessagesUnedited` |
| 5.7 | `attempts` is at least 1 in any terminal state unless `error` says the extractor was never reached. | `TestItem03DocumentKinds`, `TestItem05JudgeGroupUnreachable`, `TestS06AttemptLimit` |
| 5.8 | `service_version` is also sent as `X-Service-Version`. | checkResponse (body equals header on every job response), `TestItem03DocumentKinds` |
| 5.9 | `error` is null unless `status` is `failed`, then `{code, message}`. | `TestS06RejectUntilAttemptLimit`, `TestS06JudgeUnparseableTwice`, `TestItem04SchemaViolation` |
| 5.10 | `finished_at` is set with the terminal status. | `TestS02TimestampsRFC3339UTC`, `TestItem03DocumentKinds` |
| 5.11 | Only `accepted` is acceptance; it MUST mean the judge answered accept on `result`, `result` validates and `error` is null. | `TestS05AcceptedOnlyOnJudgeAccept`, `TestItem04SchemaViolation`, `TestItem05JudgeGroupUnreachable` |
| 5.12 | 404 when the job does not exist, has expired, or belongs to another application. | `TestS05NotFound`, `TestS03ForeignJobIs404`, `TestItem08RetentionNone`; expiry by time: not testable automatically, verified by: `internal/sweeper` TestExpiredJobsAreRemovedWithTheirBlobs |
| 5.13 | Polling MUST NOT count against the quota. | `TestS05PollingNotCharged`, `TestItem07QuotaSpent` |
| 6.1 | For one job the service MUST run the loop of section 6 and nothing else that changes the result. | `TestS06RejectUntilAttemptLimit`, `TestItem04SchemaViolation`, `TestS06JudgeUnparseableTwice` (observable steps and call counts); the absence of any other step: not testable automatically, verified by: review of `internal/extract/extract.go` |
| 6.2 | Step 1: refuse before any model call when the request or the quota fails. | `TestS06RefusalsMakeNoModelCall`, `TestS04MalformedRequests400`, `TestS04LimitsExceeded413` |
| 6.3 | Step 2: the extractor prompt is the fixed framing, the caller's `extractor_instructions` verbatim and complete, the schema, the document, and on a retry the previous candidate and its feedback, in this order. | `TestS06ExtractorPromptCarriesInputsVerbatim`, `TestItem04SchemaViolation`, `TestS06RejectUntilAttemptLimit` |
| 6.4 | The service MUST NOT shorten, rewrite or translate the caller's instructions. | `TestS06ExtractorPromptCarriesInputsVerbatim`, `TestS06JudgePromptCarriesInputsVerbatim` (byte-exact containment) |
| 6.5 | The service MUST NOT add domain content. | not testable automatically, verified by: review of the fixed framings `internal/extract/framings/*.md` (documented in `docs/operator/framings.md`) |
| 6.6 | Step 3: the extractor is called through its group with failover between endpoints. | `TestItem06PrimaryExtractorUnreachable` |
| 6.7 | The answer MAY lose code fences and surrounding prose; the service MUST NOT repair the JSON beyond that. | `TestS06NoJSONRepair` |
| 6.8 | Step 4: a candidate that does not validate, or is not JSON, goes back to step 2 with `source: "schema"` feedback, counts as an attempt and does not call the judge. | `TestItem04SchemaViolation`, `TestS06NoJSONRepair` |
| 6.9 | Step 5: the judge prompt is the judge framing, `judge_instructions` verbatim, the schema, the same document representation, the candidate. | `TestS06JudgePromptCarriesInputsVerbatim` |
| 6.10 | Step 6: an unparseable verdict is asked for once more in the same attempt; failing again, the job is `failed` with `judge_failed`. | `TestS06JudgeUnparseableTwice`, `TestS07VerdictFormat` |
| 6.11 | A judge call failing on every endpoint of its group: `failed` with `provider_unavailable`. | `TestItem05JudgeGroupUnreachable` |
| 6.12 | A judge failure never counts as acceptance. | `TestItem05JudgeGroupUnreachable`, `TestS06JudgeUnparseableTwice`, `TestS05AcceptedOnlyOnJudgeAccept` |
| 6.13 | Step 7: `accept` ends `accepted` with empty feedback; `reject` loops to the attempt limit and ends `rejected` with the last candidate and the last feedback. | `TestS06RejectUntilAttemptLimit`, `TestS05AcceptedOnlyOnJudgeAccept` |
| 6.14 | The attempt limit is per application, default 3; `max_attempts` can only lower it. | `TestS06AttemptLimit` |
| 6.15 | Every job MUST reach a terminal state within the configured limit (default 5 minutes), counted from `created_at` including queue time. | `TestS06JobTimeout` (3 s limit), `TestItem03DocumentKinds`; queue time: not testable automatically, verified by: `internal/worker` TestDeadlinePassedAtLease and `internal/sweeper` TestStaleRunningJobsAreFinishedAsTimeout |
| 6.16 | The limit MUST NOT exceed 8 minutes. | `TestS12ConfigCheck` (`job_timeout: 9m` is refused) |
| 6.17 | On expiry the job is `failed` with `timeout`, keeping any candidate and feedback so far. | `TestS06JobTimeout` |
| 6.18 | Endpoint failover is bounded in tries and total time and never crosses into the other role's group. | `TestItem05JudgeGroupUnreachable` (no judge call reaches an extractor endpoint), `TestItem06PrimaryExtractorUnreachable`, `TestS06JobTimeout`; the bounds themselves: not testable automatically, verified by: `internal/router` TestGroup_Complete and TestGroup_Complete_maxTotalTime |
| 7.1 | The judge framing MUST ask for, and the service MUST parse, exactly `{"verdict": …, "feedback": {…}}`. | `TestS07VerdictFormat`, `TestS05AcceptedOnlyOnJudgeAccept` |
| 7.2 | `verdict` is `accept` or `reject`; anything else is unparseable. | `TestS07VerdictFormat`, `TestS05AcceptedOnlyOnJudgeAccept` (`"accepted"`) |
| 7.3 | A `reject` MUST have at least one feedback entry. | `TestS05AcceptedOnlyOnJudgeAccept` (a reject without entries is never accepted) |
| 7.4 | The feedback map becomes the `feedback` array in the order the judge gave, with `source: "judge"`. | `TestS07FeedbackOrderAndMessagesUnedited` |
| 7.5 | The service MUST NOT edit messages, except to cap their length (cap of at least 2,000 characters). | `TestS07FeedbackOrderAndMessagesUnedited` (2,500 characters, tabs, newlines, non-ASCII) |
| 7.6 | The service's framing MUST agree with the format the caller's instructions restate. | `TestS07VerdictFormat` (the framing names the same keys and values); wording: not testable automatically, verified by: comparing `internal/extract/framings/judge.md` with `testdata/travelinbox/prompts/judge.md` |
| 8.1 | Quota: submissions count; polls and quota reads do not. | `TestS08QuotaRead`, `TestS05PollingNotCharged`, `TestItem07QuotaSpent` |
| 8.2 | A spent quota answers `POST /v1/jobs` with 429 and `X-Quota-Remaining: 0`. | `TestItem07QuotaSpent` |
| 8.3 | A rate limit answers 429 with `X-Quota-Remaining` above zero. | `TestS08RateLimited` |
| 8.4 | Every response to an authenticated request, 429 and errors included, MUST carry `X-Quota-Remaining` and `X-Quota-Reset` (RFC 3339 UTC). | `TestS08QuotaHeadersOnEveryAuthenticatedResponse`, `TestS08QuotaHeadersOnForbidden`; checkResponse |
| 8.5 | Every 429 MUST carry `Retry-After`. | `TestItem07QuotaSpent`, `TestS08RateLimited`; checkResponse |
| 8.6 | `GET /v1/quota` MUST return 200 with the same numbers as a body, without changing them. | `TestS08QuotaRead`, `TestItem07QuotaSpent` |
| 9.1 | Every error response has the `{"error": {code, message, request_id}}` body and `Content-Type: application/json`. | `TestS09ErrorCodes`; checkResponse on every error of the suite |
| 9.2 | HTTP error codes: `bad_request` 400, `unauthorized` 401, `forbidden` 403, `not_found` 404, `payload_too_large` 413, `unsupported_media_type` and `unsupported_document` 415, `invalid_schema` and `idempotency_mismatch` 422, `rate_limited` and `quota_exhausted` 429. | `TestS09ErrorCodes` |
| 9.3 | `internal` 500, `provider_unavailable` 502, `unavailable` 503, `timeout` 504 at request time. | checkResponse (a code is never sent with another status); provoking them: not testable automatically, verified by: `internal/api` TestOpenAPIDescribesErrorCodes and the handler unit tests |
| 9.4 | Job error codes: `provider_unavailable`, `extractor_failed`, `judge_failed`, `timeout`, `internal`, with `result` as the table says. | `TestItem05JudgeGroupUnreachable`, `TestS06NoJSONRepair`, `TestS06JudgeUnparseableTwice`, `TestS06JobTimeout`; `internal`: not testable automatically, verified by: `internal/extract` unit tests |
| 10.1 | `retention: none`: the document and pages MUST be deleted as soon as the job is terminal. | `TestItem08RetentionNone` (blobs gone before any read), `TestS10OperatorForcesRetentionNone` |
| 10.2 | `retention: none`: result, feedback and error MUST be deleted on the first GET returning a terminal status, or 24 hours after `created_at`; then the job is 404. | `TestItem08RetentionNone`; the 24-hour path: not testable automatically, verified by: `internal/sweeper` TestExpiredJobsAreRemovedWithTheirBlobs |
| 10.3 | `expires_at` reports the 24-hour bound. | `TestItem08RetentionNone` |
| 10.4 | Nothing of the job's content survives in the database, object store, queue or cache afterwards; only the audit record. | `TestS10NoContentSurvivesFirstRead` (D1 fixed: WAL checkpoint after each content deletion); `internal/job` TestDeletedContentLeavesNoTraceInDatabaseFiles. Freed disk blocks: not testable automatically, see `docs/security.md` |
| 10.5 | `retention: default` is the operator's configured retention for the application. | `TestS10DefaultRetention` |
| 10.6 | The operator MUST be able to force `none` for an application whatever requests say. | `TestS10OperatorForcesRetentionNone` |
| 10.7 | Document bytes, pages, instructions, schema, candidates, results and feedback MUST NOT appear in logs, traces, metrics or error messages at any level. | `TestS10NoContentInLogsOrMetrics` (debug level), `TestS02ErrorMessagesQuoteNoContent`; traces: the service emits none |
| 10.8 | A debug mode that logs content MUST be off by default and documented as unsafe for production. | `TestS10NoContentInLogsOrMetrics` (default configuration logs none); the documentation: not testable automatically, verified by: `docs/operator/configuration.md` (`debug.log_content`) |
| 10.9 | The service MUST document, per provider adapter, what it sends and which retention or training settings it sets. | not testable automatically, verified by: `docs/providers/*.md`, kept complete by `internal/provider` TestEveryProviderIsDocumented |
| 10.10 | Only operator-approved endpoints receive documents; a request can never name an endpoint or model. | `TestS10RequestCannotNameEndpoint`; every call the fake provider records reached a configured endpoint |
| 10.11 | Transport is HTTPS; stored jobs are encrypted at rest when the storage supports it. | `TestS02HTTPSRequiredOutsideLoopback`; at rest: not testable automatically, verified by: the deployment's volume encryption (`docs/operator/install.md`, `docs/security.md`) |
| 11.1 | `service_version` MUST identify the release, the role configuration (groups, models, escalation, attempt limit) and the framings: same string, same configuration. | `TestS11ServiceVersionTracksConfiguration`; release and framings: not testable automatically, verified by: `internal/extract` TestServiceVersion_stability and TestServiceVersion_framing |
| 11.2 | `service_version` MUST be in the job body and in `X-Service-Version` of every `/v1/jobs` response. | checkResponse on every `/v1/jobs` response, errors and 401 included (`TestItem02Unauthenticated`, `TestS09ErrorCodes`) |
| 11.3 | Within `/v1` changes are additive; a breaking change ships as `/v2`, and `/v1` stays 12 months. | not testable automatically, verified by: the release process and `internal/api` TestOpenAPISupersetOfTravelInbox |
| 12.1 | `GET /healthz`: 200 `{"status": "ok"}` without authentication when the service can accept jobs, else 503. | `TestS12Healthz`, `TestItem05JudgeGroupUnreachable` (503 with the judge group down) |
| 12.2 | Audit log of token and job events, never content. | not testable automatically, verified by: `internal/audit` TestNoContentInEventsOrLogs and TestJobHelpers; no content in the database file: `TestS10NoContentSurvivesFirstRead` |
| 12.3 | `vde config check` MUST validate without network calls: groups reference defined endpoints, each endpoint-model pair declares its media types, both roles have a group. | `TestS12ConfigCheck` |
| 12.4 | Configuration is one YAML file plus environment variables for secrets. | `TestS12DevConfigDocumented`, `TestMain` (one file, keys by `api_key_env`) |
| 12.5 | One container image with the server as entrypoint; the same binary offers `serve`, `token` and `config check`; the image MUST run on `linux/arm64` and `linux/amd64`. | `TestS12ConfigCheck`, `TestItem02Unauthenticated` (one binary for all three); the image: not testable automatically, verified by: the CI `cross` job (static linux/amd64 and linux/arm64 binaries) and `make image-multiarch` on the release branch |
| 12.6 | A minimal development configuration MUST be documented: one provider, one model for both roles, one application and token, one key in an environment variable. | `TestS12DevConfigDocumented` |

### SHOULDs also checked

- 2: every response carries `X-Request-Id`, echoing the client's: `TestS02RequestID`, checkResponse.
- 4: `image/webp` is accepted as a type (refused 415 `unsupported_document` here only because no configured model reads it): `TestS04UnsupportedDocument`.
- 7: the message cap is at least 2,000 characters: `TestS07FeedbackOrderAndMessagesUnedited`.
- 14: the job body is a superset of the TravelInbox `Job` schema: `TestJobBodyMatchesOpenAPI`.

### Contract points the suite reads one way

- Item 6 says the only visible difference through the fallback is latency. `service_version`
  is per configuration and does not change with the endpoint that answered, so the test
  compares status, attempts, result, feedback and error with what the primary would give.
- Section 5 says `feedback` is "the final feedback on `result`"; section 6 says a timeout
  keeps "any candidate and feedback produced so far". When the limit cuts the judge call on
  the last candidate, there is no feedback on that candidate yet: `TestS06JobTimeout`
  accepts an empty `feedback` then and checks that any entry is the judge's.
- Section 5 says `feedback` is empty on `accepted`, section 7 that an accept's advisory
  entries are forwarded. The suite does not assert either for an accept carrying entries.

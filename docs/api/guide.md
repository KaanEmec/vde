# Calling the service

A task-oriented guide to the vde HTTP API. The reference is
[`api/openapi.yaml`](../../api/openapi.yaml) (also served at `GET /v1/openapi.yaml`) and the
conventions are in the [README](README.md); this page walks through a call from start to
finish. Writing the instructions and the schema you send is a topic of its own:
[writing-instructions.md](writing-instructions.md). Runnable clients are in
[`examples/`](examples/).

The flow is always the same: **submit** a document and get a job back at once, **poll** the
job until it reaches a terminal status, **read** `result` and `feedback`.

> **The examples have not been run against a live deployment yet** (the handlers are still
> being wired). They follow the contract and the OpenAPI document exactly; syntax checks
> are all they have had so far.

## Where a client may run

The bearer token is a service credential. Hold it only in server-side code: a backend, a
worker, a script. A browser page or a shipped mobile app must never contain it; such
clients talk to your own backend, which calls vde. The Swift example is meant for
server-side Swift or for code behind your own backend, not for an app that embeds the token.

## Authentication

Every `/v1/*` request carries `Authorization: Bearer <token>`. The operator issues tokens on
the command line (see [the compose guide](../../examples/compose/README.md)):

```
vde token create --app dev-app --scope jobs --expires 2027-12-31 --label "my worker"
```

The token is printed once; the service stores only its hash. Every token has an expiry, an
application may have several active tokens (create the new one before revoking the old
one), and revocation takes effect at once.

| Scope | Allows |
| --- | --- |
| `jobs` | Submit, poll, delete and read the quota. |
| `jobs:read` | Poll and read the quota only; submitting is `403`. Useful for a dashboard that only watches jobs. |

| Situation | Answer |
| --- | --- |
| No token, or an unknown, expired or revoked one | `401 unauthorized` |
| A valid token used outside its scope | `403 forbidden` |
| Another application's job id | `404 not_found` (job ids leak nothing) |

HTTPS is required except from loopback (`http://127.0.0.1:8090` works for development); a
plain-HTTP request from elsewhere answers `400 bad_request`.

## Submit: `POST /v1/jobs`

`multipart/form-data`, parts in any order. Part names are exact; a repeated or missing
required part is `400 bad_request`.

| Part | Count | Content-Type | Content |
| --- | --- | --- | --- |
| `document` | 1 | the document's media type, parameters allowed (`text/plain; charset=utf-8`) | The document to read. |
| `page` | 0 to 20 | `image/png` or `image/jpeg` | A rendered page, repeated, page 1 first. Only for documents with pages (PDF); none for text, HTML or a single image. |
| `extractor_instructions` | 1 | `text/markdown` or `text/plain` | What to extract and how. |
| `judge_instructions` | 1 | `text/markdown` or `text/plain` | What counts as faithful. |
| `schema` | 0 or 1 | `application/schema+json` | JSON Schema 2020-12 the result must satisfy. Optional; without it see [schema-less mode](#schema-less-mode). |
| `retention` | 0 or 1 | `text/plain` | `none` or `default`. See [Retention](#retention). |
| `max_attempts` | 0 or 1 | `text/plain` | A positive integer that lowers the attempt limit for this job. Higher values are clamped, not refused. |

Accepted document types: `application/pdf`, `image/jpeg`, `image/png`, `image/webp`,
`image/heic`, `text/html`, `text/plain`, `text/markdown`, `message/rfc822`. A
`message/rfc822` document is an email: its body is read and the job lists the email's
attachments (name, media type, size) in `attachments`; the attachments themselves are not
read, so submit each one as a job of its own if you need them.

You never choose the model or the representation. The service decides from its
configuration whether the model reads the document itself or the `page` images, and shows
the judge the same thing. A document that no configured model can read is refused at submit
with `415 unsupported_document`.

| Limit | Value |
| --- | --- |
| `document` | 25 MiB |
| each `page` | 10 MiB |
| `page` count | 20 |
| each instructions part | 64 KiB |
| `schema` | 256 KiB |
| whole request | 128 MiB |

The operator may raise these. Over any of them: `413 payload_too_large`.

The answer is `202 Accepted` with `Location: /v1/jobs/{id}` and the job, `status: "queued"`.

```
curl -sS "$EXTRACTION_URL/v1/jobs" \
  -H "Authorization: Bearer $EXTRACTION_TOKEN" \
  -H "Idempotency-Key: $(shasum -a 256 booking.txt | cut -d' ' -f1)" \
  -F "document=@booking.txt;type=text/plain; charset=utf-8" \
  -F "extractor_instructions=<extractor.md;type=text/markdown" \
  -F "judge_instructions=<judge.md;type=text/markdown" \
  -F "schema=<schema.json;type=application/schema+json" \
  -F "retention=none"
```

`-F name=<file` sends the file's contents as the part's value; `-F name=@file` sends it as a
file upload. Both work for the text parts; the examples use `<` as the contract does.

## Poll: `GET /v1/jobs/{id}`

Polling does not count against your quota. Start at **2 seconds** and double up to a ceiling
of **30 seconds**, with a little jitter, until `status` is terminal or you have waited as long
as you are willing to (the service itself ends every job within its time limit, 5 minutes by
default, so waiting 10 minutes is more than enough):

```
2 s, 4 s, 8 s, 16 s, 30 s, 30 s, ...
```

A poll that fails with `429`, a `5xx` or a transport error is repeated; give up after three
failures in a row. A `404` while polling means the job expired, was deleted, or (with
`retention: none`) you already read its terminal state.

### The job

```json
{
  "id": "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB",
  "status": "rejected",
  "result": {"title": "Harbour View Hotel booking confirmation", "...": "..."},
  "feedback": [
    {"pointer": "/total/amount", "message": "The document says 412.50, not 421.50.", "source": "judge"}
  ],
  "attempts": 3,
  "service_version": "vde-0.2.0+c0ffee12",
  "judge_confidence": 0.82,
  "error": null,
  "created_at": "2026-10-16T09:12:03Z",
  "finished_at": "2026-10-16T09:13:30Z",
  "expires_at": "2026-10-17T09:12:03Z"
}
```

| Field | Meaning |
| --- | --- |
| `status` | `queued`, `running`, then exactly one terminal status: `accepted`, `rejected` or `failed`. A terminal status never changes. |
| `result` | The last candidate the extractor produced, parsed. `null` while in progress and when no attempt produced JSON. On `rejected` and `failed` it is kept as produced, valid or not, so nothing is lost. |
| `feedback` | Final feedback on `result`; empty on `accepted` and while in progress (see below). |
| `attempts` | Extractor calls made. |
| `service_version` | Identifies the release and role configuration that produced the result. Store it with the result; two results with the same string came from the same setup. Also in the `X-Service-Version` header. |
| `judge_confidence` | The judge's optional confidence in its verdict, 0 to 1, or `null`. Advisory; it never changes the status. |
| `error` | `null` unless `status` is `failed`: `{"code", "message"}`. |
| `attachments` | For `message/rfc822` documents only: `[{"name", "media_type", "bytes"}]`. |
| `expires_at` | When the job becomes `404`. |

### Interpreting the status

Only `accepted` is acceptance. It means the judge accepted `result`, `result` validates
against your schema (when you sent one) and `error` is `null`. Treat every other
combination, including a status you do not know, as not accepted: keep `result` and
`feedback`, and send the document to a person.

| Status | Meaning | What to do |
| --- | --- | --- |
| `queued`, `running` | In progress. | Keep polling. |
| `accepted` | The judge accepted a result that validates. | Use `result`. `feedback` may carry advisory entries from the judge. |
| `rejected` | The attempt limit was reached and the judge (or the schema check) still found problems. | Show or log `feedback`, keep `result` for review. Resubmitting the same document with the same instructions rarely helps; improve the instructions or route to a person. |
| `failed` | The service could not finish: see `error.code`. | `provider_unavailable`, `timeout`, `internal`: retry later with a new submission. `extractor_failed`, `judge_failed`: the models answered unusably; retry once, then review by hand. Unknown codes: treat as a service failure and retry later. |

### Feedback entries

Each entry has three fields:

- `pointer`, an RFC 6901 JSON pointer into `result`. `""` means the whole result.
  `/line_items/1/amount` is one value; a pointer ending in `/-` (for example
  `/line_items/-`) means something is missing from that array.
- `message`, text meant for a human. It may quote the source document, so treat it like
  document content: do not log it where documents must not go.
- `source`: `schema` when the result failed validation against your schema (one entry per
  violation, produced by the service, no judge involved), `judge` when the judge raised it.

On `rejected` the feedback is the last round's. On `accepted` the judge may have added
advisory entries; they do not block acceptance. The pointer is relative to `result` exactly
as returned, so you can attach each message to the field it concerns.

## Idempotency

Send `Idempotency-Key: <1 to 128 characters>` on `POST /v1/jobs`. A repeat of the request
with the same key from the same application within 24 hours returns the existing job with
`Idempotency-Replayed: true` and runs nothing, so a retry after a lost response never pays
for a second extraction. The same key with a different body is `422 idempotency_mismatch`.

Derive the key from what makes the request the same: a hash of the document bytes plus the
version of your instructions and schema. A random key per attempt defeats the purpose; a
key per document and version makes every retry safe.

## Quota and rate limits

Two mechanisms, both answering `429`:

- **Quota**: submissions per window (for example a day). Polls and quota reads are free.
  Spent: `429 quota_exhausted` with `X-Quota-Remaining: 0`.
- **Rate limit**: a short-term ceiling on requests, submissions and jobs in progress.
  `429 rate_limited` with `X-Quota-Remaining` above zero.

Every authenticated response, errors included, carries `X-Quota-Remaining` (jobs you may
still submit in this window) and `X-Quota-Reset` (RFC 3339 UTC). `GET /v1/quota` returns the
same numbers as a body, `{"limit", "remaining", "reset", "window"}`, and is a cheap way to
verify a credential at start-up.

Every `429` has `Retry-After` (seconds or an HTTP date):

| `429` | Do this |
| --- | --- |
| `X-Quota-Remaining: 0` (`quota_exhausted`) | Stop submitting until `X-Quota-Reset`. Hold your pending documents; do not count the wait as a failed attempt. |
| Anything else (`rate_limited`) | Wait `Retry-After`, then submit again; bound the number of tries. |

## Retention

- `retention: none`: the document and pages are deleted as soon as the job is terminal, and
  the result, feedback and error are deleted on the **first `GET` that returns a terminal
  status** (or 24 hours after `created_at`, whichever is first). After that the job is
  `404`. So store the result when you read it; there is no second read. If your client
  crashes between the response and storing it, that job's content is gone and the
  extraction must be run again, which should use a new idempotency key (a replay of the old key may only
  lead back to the deleted job).
- `retention: default` (or the part absent): the operator's configured retention for your
  application, 24 hours unless changed. You can read the job repeatedly until `expires_at`.
- The operator can force `none` for an application whatever you send.

`DELETE /v1/jobs/{id}` deletes the document, pages, result and feedback at once (`204`);
later reads are `404`.

## Errors

Every error has one shape and `Content-Type: application/json`:

```json
{"error": {"code": "quota_exhausted", "message": "The daily quota of 2000 jobs is spent.", "request_id": "r_01JA3V9Q6ZK7M4W8X2R5T1N0HB"}}
```

`message` is a fixed text that never quotes your documents, instructions or schema.
`request_id` repeats the `X-Request-Id` response header; send your own `X-Request-Id` (1 to
128 visible ASCII characters) to correlate with your logs, and quote it to the operator.

| Status | `error.code` | Cause | Retry? |
| --- | --- | --- | --- |
| 400 | `bad_request` | Malformed multipart, missing or repeated part, bad `retention` or `max_attempts`, or plain HTTP where HTTPS is required. | No: fix the request. |
| 401 | `unauthorized` | Missing, unknown, expired or revoked token. | No, until the credential is fixed or rotated. |
| 403 | `forbidden` | Token outside its scope. | No. |
| 404 | `not_found` | Unknown, expired, deleted or foreign job (or an unknown route). | No. |
| 405 | `method_not_allowed` | Wrong method on a known route (`Allow` lists the right ones). | No. |
| 413 | `payload_too_large` | Over a size or count limit. | No. |
| 415 | `unsupported_media_type` | Document media type never accepted. | No. |
| 415 | `unsupported_document` | Accepted type, but no configured model can read this document. | No. |
| 422 | `invalid_schema` | `schema` is not valid JSON Schema 2020-12. | No: fix the schema. |
| 422 | `idempotency_mismatch` | Same `Idempotency-Key`, different request. | No: use a new key for a changed request. |
| 429 | `rate_limited` | Short-term ceiling. | Yes, after `Retry-After`. |
| 429 | `quota_exhausted` | Quota spent, `X-Quota-Remaining: 0`. | Yes, after `X-Quota-Reset`. |
| 500 | `internal` | Service failure. | Yes, with backoff. |
| 502 | `provider_unavailable` | Model provider failure. | Yes, with backoff. |
| 503 | `unavailable` | Service not ready. | Yes, with backoff. |
| 504 | `timeout` | Time limit. | Yes, with backoff. |

The rule of thumb: `400`, `413`, `415` and `422` mean the request itself is wrong, so do not
retry them. `401` and `403` are a configuration problem you or the operator must fix.
`429` and `5xx` are retried later. A code you do not know is a service failure: retry
later. Retry a submission with the **same** `Idempotency-Key` so it cannot run twice.
Suggested backoff for service failures: 30 s, 1, 2, 4 minutes, then give up.

## Schema-less mode

Leave out the `schema` part and the job runs schema-less: the extractor returns whatever
JSON the instructions describe (one object, or an array when they ask for a list), no schema
validation and no `source: "schema"` feedback are involved, and acceptance is the judge's
verdict alone. `result` is then any JSON object as the extractor produced it. Use it for
exploration, one-off documents or shapes that are not worth a schema; prefer a schema
whenever your code depends on the shape, since nothing but the judge checks it.

Because there is no schema to say what to return, the instructions must. The extractor
instructions name the fields and their formats; the judge instructions say that fields the
extractor instructions ask for and the candidate lacks are completeness problems.

```
curl -sS "$EXTRACTION_URL/v1/jobs" \
  -H "Authorization: Bearer $EXTRACTION_TOKEN" \
  -F "document=@booking.txt;type=text/plain; charset=utf-8" \
  -F 'extractor_instructions=Return one JSON object with "reference" (the confirmation number, or null), "guest" (the guest name) and "nights" (an integer). Use only what the document states.;type=text/plain' \
  -F 'judge_instructions=Accept when reference, guest and nights match the document. Reject otherwise, with the pointer of each wrong field.;type=text/plain' \
  -F "retention=none"
```

(A literal-text part has no filename, so the shorter `name=value;type=...` form is fine for
small instructions; use `<file` for anything longer.) For the sample
`examples/samples/booking.txt` an accepted job looks like:

```json
{
  "id": "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB",
  "status": "accepted",
  "result": {"reference": "HV-48213", "guest": "Jane Example", "nights": 3},
  "feedback": [],
  "attempts": 1,
  "service_version": "vde-0.2.0+c0ffee12",
  "error": null,
  "created_at": "2026-10-16T09:12:03Z",
  "finished_at": "2026-10-16T09:12:11Z",
  "expires_at": "2026-10-17T09:12:03Z"
}
```

## Other things worth knowing

- `X-Service-Version` is on every `/v1/jobs` response, errors included.
- Responses are `Cache-Control: no-store`.
- Clients ignore fields they do not know: changes within `/v1` are additive only.
- `GET /healthz` is unauthenticated and answers `200 {"status":"ok"}` when the service can
  accept jobs, or `503` with `reasons`.

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
| `judge` | 0 or 1 | `text/plain` | `always`, `never` or `auto`: the [judge policy](#judge-policy) of this job. Only when the operator allows it for your application; otherwise `400 bad_request`. |

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

### JSON instead of multipart

Where multipart is awkward, send the same fields as `Content-Type: application/json` (v0.3).
The document and pages are standard base64; `schema` is a JSON object, not a string;
`max_attempts` is a JSON integer. The limits above apply to the decoded bytes, and the
checks, refusals and idempotency rules are those of the multipart path (the whole JSON body
counts against the request limit, so base64's overhead leaves less room). Unknown fields
are `400`.

```json
{
  "document": {"media_type": "text/plain; charset=utf-8", "data_base64": "QWRhIExvdmVsYWNl...", "filename": "booking.txt"},
  "pages": [{"media_type": "image/png", "data_base64": "iVBORw0K..."}],
  "extractor_instructions": "Extract the traveller's name.",
  "judge_instructions": "The name must appear in the document.",
  "schema": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}},
  "retention": "none",
  "max_attempts": 2
}
```

Instead of `data_base64` the document may be `"url": "https://..."` when the operator turned
on `documents.fetch`: the service fetches it once, at submit, under a size and time limit.
Only `https` URLs on the operator's `allowed_hosts` are fetched; redirects are followed only
to the same host; a host that resolves to a loopback, private or link-local address is
refused, checked on the connection itself after DNS resolution. The `media_type` you declare
is used, not the server's `Content-Type`. A refused or failed fetch is `400`, an oversized
document `413`. The fetch happens before the submission rate limit and quota are applied, so
each fetch costs one request against the request rate limit only, even when the
submission is then refused.

## Synchronous: `POST /v1/extract`

For small documents and interactive callers (contract §16; TravelInbox does not use it).
The request is exactly that of `POST /v1/jobs`, multipart or JSON, with the same limits,
quota, submission rate limit and in-progress ceiling. The service holds the response open
until the job is terminal, up to its `sync.max_wait` (60 s by default, never more than
120 s):

- `200` with the terminal job (and `Location`). This is the job's first read: a
  `retention: none` job's content is deleted, and a later `GET` is `404`.
- `202` with the job as it is (queued or running) and `Location` when the wait ran out;
  poll it as usual. The job keeps running if you hang up early.
- A repeated `Idempotency-Key` answers as `POST /v1/jobs` does: `202` with the existing
  job and `Idempotency-Replayed: true`, without waiting.

Set your client timeout above the service's `sync.max_wait`. Behind several replicas the
wait still works: it reads the job from the store every second.

## Completion webhooks

The operator can give an application a webhook (`applications[].webhook`); a request
never names a URL. When a job reaches a terminal status the service POSTs to that URL:

```
POST /hooks/vde HTTP/1.1
Content-Type: application/json
X-VDE-Event: job.finished
X-VDE-Signature: t=1792143123,v1=6f1c...e2

{"event":"job.finished","job_id":"j_01JA3V9Q6ZK7M4W8X2R5T1N0HB","application_id":"travelinbox",
 "status":"accepted","service_version":"vde-0.3.0+c0ffee12","finished_at":"2026-10-16T09:12:11Z"}
```

The body never carries `result`, `feedback` or `error`: fetch `GET /v1/jobs/{id}`, so
retention works as for a polled job. Verify every delivery before acting on it:

1. Split `X-VDE-Signature` on `,` into `t=<unix seconds>` and `v1=<hex>`.
2. Compute HMAC-SHA256 with the shared secret over the bytes `<t>.<raw body>` (the body
   exactly as received, before any JSON parsing) and compare it to `v1` in constant time.
3. Replay protection: refuse a delivery whose `t` is more than a few minutes (say five)
   from your clock. The signature covers `t`, so an attacker cannot refresh it; within the
   window, deduplicate on `job_id` (a retry of the same delivery carries the same body and
   a new `t`).

`Verify` in the service's `internal/webhook` package is a reference implementation. Answer any
`2xx` quickly; anything else, a timeout or a redirect (not followed) is retried, up to five
attempts over about 15 minutes. Deliveries are held in memory, so a restart of the service
can drop one: keep polling as the fallback, at a slower pace if you like.

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
  "verification": "none",
  "error": null,
  "created_at": "2026-10-16T09:12:03Z",
  "finished_at": "2026-10-16T09:13:30Z",
  "expires_at": "2026-10-17T09:12:03Z"
}
```

| Field | Meaning |
| --- | --- |
| `status` | `queued`, `running`, then exactly one terminal status: `accepted`, `rejected` or `failed` (or `extracted` under [judge policy](#judge-policy) `never`). A terminal status never changes. |
| `result` | The last candidate the extractor produced, parsed. `null` while in progress and when no attempt produced JSON. On `rejected` and `failed` it is kept as produced, valid or not, so nothing is lost. |
| `feedback` | Final feedback on `result`; empty on `accepted` and while in progress (see below). |
| `attempts` | Extractor calls made. |
| `service_version` | Identifies the release and role configuration that produced the result. Store it with the result; two results with the same string came from the same setup. Also in the `X-Service-Version` header. |
| `judge_confidence` | The judge's optional confidence in its verdict, 0 to 1, or `null`. Advisory; it never changes the status. |
| `verification` | Added by vde: what accepted `result`, `judge`, `grounded` or `none`; `null` while queued or running. See [Judge policy](#judge-policy). |
| `error` | `null` unless `status` is `failed`: `{"code", "message"}`. |
| `attachments` | For `message/rfc822` documents only: `[{"name", "media_type", "bytes"}]`. |
| `expires_at` | When the job becomes `404`. |
| `content_deleted_at` | Added by vde; absent on every job you can poll. Only an idempotent replay of a job whose content is gone carries it (see [Idempotency](#idempotency)). |

### Interpreting the status

Only `accepted` is acceptance. It means the judge accepted `result`, or, only for an
application whose operator opted into judge policy `auto`, that the grounding check found
every critical value in the document; `verification` says which (`judge` or `grounded`).
In both cases `result` validates against your schema (when you sent one) and `error` is
`null`. Treat every other combination, including a status you do not know, as not
accepted: keep `result` and `feedback`, and send the document to a person. `extracted` is
such a status: no judge looked at the result.

| Status | Meaning | What to do |
| --- | --- | --- |
| `queued`, `running` | In progress. | Keep polling. |
| `accepted` | The judge accepted a result that validates (`verification: judge`), or under an opted-in `auto` the grounding check did (`verification: grounded`). | Use `result`. `feedback` may carry advisory entries from the judge. If you require a judge's acceptance, also check `verification` is `judge`. |
| `rejected` | The attempt limit was reached and the judge (or the schema check) still found problems. | Show or log `feedback`, keep `result` for review. Resubmitting the same document with the same instructions rarely helps; improve the instructions or route to a person. |
| `extracted` | Added by vde, only under judge policy `never`: the final result validates, no judge was asked. Never acceptance. | Treat as not accepted: review `result` yourself. `feedback` may carry advisory `grounding` entries naming values not found in the document. |
| `failed` | The service could not finish: see `error.code`. | `provider_unavailable`, `timeout`, `internal`: retry later with a new submission. `extractor_failed`, `judge_failed`: the models answered unusably; retry once, then review by hand. Unknown codes: treat as a service failure and retry later. |

### Feedback entries

Each entry has three fields:

- `pointer`, an RFC 6901 JSON pointer into `result`. `""` means the whole result.
  `/line_items/1/amount` is one value; a pointer ending in `/-` (for example
  `/line_items/-`) means something is missing from that array.
- `message`, text meant for a human. It may quote the source document, so treat it like
  document content: do not log it where documents must not go.
- `source`: `schema` when the result failed validation against your schema (one entry per
  violation, produced by the service, no judge involved), `judge` when the judge raised it,
  or `grounding` (added by vde, judge policies `never` and `auto` only) when the grounding
  check did not find the value in the document: the message is always "value not found in
  the document" and never quotes the value. Treat a source you do not know like `judge`.

On `rejected` the feedback is the last round's. On `accepted` the judge may have added
advisory entries; they do not block acceptance. On `extracted` the `grounding` entries are
advisory. Under the default policy `always` you never see `grounding` entries. The pointer is relative to `result` exactly
as returned, so you can attach each message to the field it concerns.

## Judge policy

Added by vde in v0.4 ([ADR 0009](../architecture/decisions/0009-judge-policy-and-grounding.md);
the operator's side is in [judge policy](../operator/judge-policy.md)). The operator sets per
application when the judge is asked:

- `always` (the default): every schema-valid candidate goes to the judge, and `accepted`
  means the judge accepted it. This is contract v1; `verification` is `judge` on
  `accepted` and `none` otherwise.
- `never`: no judge. A schema-valid final candidate ends in the terminal status
  `extracted`, which is never acceptance. Treat it like any status you do not know.
- `auto`: a candidate whose critical values are all found in the document text may be
  accepted without a judge, with `verification: grounded` and `judge_confidence: null`;
  everything else (documents without text, such as images, included), and a small sample,
  goes to the judge, and ends as under `always`.

| Policy | Possible terminal statuses | `verification` |
| --- | --- | --- |
| `always` | `accepted`, `rejected`, `failed` | `judge` on `accepted`, else `none` |
| `never` | `extracted`, `rejected` (schema violations to the last attempt), `failed` | `none` |
| `auto` | `accepted`, `rejected`, `failed` | `judge` or `grounded` on `accepted`, else `none` |

Your schema can steer the check with the annotation `x-vde-grounding`
([writing instructions](writing-instructions.md#annotating-the-schema-for-grounding)).

When the operator sets `judge.allow_override` for your application you may choose the
policy per job with the `judge` part (or JSON field `judge`). Without that setting the part
is refused with `400 bad_request` ("The judge part is not allowed: ..."), whatever its
value; an unknown value, a media type other than `text/plain` or more than 16 bytes is
also `400`. The
part counts towards the [idempotency](#idempotency) body hash, and the job's
`service_version` is the one of the policy it runs under. Under `never` and `auto`,
`feedback` may carry entries with `source: "grounding"`: a value the grounding check did not
find in the document.

## Idempotency

Send `Idempotency-Key: <1 to 128 characters>` on `POST /v1/jobs`. A repeat of the request
with the same key from the same application within 24 hours returns the existing job with
`Idempotency-Replayed: true` and runs nothing, so a retry after a lost response never pays
for a second extraction. The same key with a different body is `422 idempotency_mismatch`.

A replay of a finished `retention: none` job whose result you have not read yet deletes
nothing: it returns the job's terminal `status` without its content (`result` null,
`feedback` empty, `error` null, no `content_deleted_at`). Read the result with
`GET /v1/jobs/{id}`, which is the first read and deletes it (so a poll timeout followed by a
resubmission with the same key still gets the result); once it is read, replays return the
content-deleted form below.

The 24 hours hold even when the job itself is no longer readable. A replayed job whose
content was already read (`retention: none`, first terminal read) or has expired has
`content_deleted_at` set and `result` null: `status` is the job's terminal status as it
was, `feedback` is empty and `error` is null. Treat it as already delivered: you stored the
result when you first read it, or it expired unread. Do not re-submit with the same key
(you would get the same answer); if you really need the extraction again, use a new key.

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
  extraction must be run again, which should use a new idempotency key (a replay of the old key leads
  back to the deleted job: `202` with `content_deleted_at` set and `result: null`).
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
| 400 | `bad_request` | Malformed multipart, missing or repeated part, bad `retention`, `max_attempts` or `judge`, a `judge` part the application does not allow, or plain HTTP where HTTPS is required. | No: fix the request. |
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
| 503 | `unavailable` | Service not ready, or saturated: the operator's queue ceiling (`limits.max_queued_jobs`) or in-flight submission limit is reached and `Retry-After` is set. Nothing was stored or charged. | Yes, after `Retry-After`, with backoff. |
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

## Stability

What this guide describes is frozen for `/v1` from 1.0.0, except what the API document
marks `x-stability: experimental`: the [completion webhook](#completion-webhooks) and the
document by `url`. The [compatibility policy](../compatibility.md) has the details. In
short, within `/v1` fields, statuses, error codes and headers are only ever added, so a
client that ignores unknown fields and treats unknown statuses, codes and sources as this
guide says keeps working. The grounding rules are not part of the promise: a change to
them changes `service_version`.

## Other things worth knowing

- `X-Service-Version` is on every `/v1/jobs` and `/v1/extract` response, errors included.
- Responses are `Cache-Control: no-store`.
- Clients ignore fields they do not know: changes within `/v1` are additive only
  ([compatibility policy](../compatibility.md)).
- `GET /healthz` is unauthenticated and answers `200 {"status":"ok"}` when the service can
  accept jobs, or `503` with `reasons`.

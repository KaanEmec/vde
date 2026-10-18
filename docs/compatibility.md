# Compatibility policy

What a client of `/v1` can rely on from vde 1.0.0 on, and how the API changes. It applies
contract §11: within `/v1` changes are additive only, and a breaking change ships as `/v2`
with `/v1` available for at least 12 months after. The reference is
[`api/openapi.yaml`](../api/openapi.yaml); the document as frozen at 1.0.0 is
[`api/openapi.v1.0.0.yaml`](../api/openapi.v1.0.0.yaml), and `TestOpenAPISupersetOfSnapshot`
(`internal/api`) fails when the current document stops being a superset of it.

The admin API (`/admin/v1`), configuration keys, metrics series, the CLI and log lines are
not covered here: they are operator surfaces with their own rules (configuration:
[config freeze](roadmap/v1.0-stable/01-api-freeze/02-config-freeze.md)).

## Stable and experimental

Every operation in the API document carries `x-stability: stable` or `experimental`; a
field without its own marker has its operation's. Everything stable is frozen as below.
Something experimental may change or be removed in a minor release, announced in the
CHANGELOG; it becomes stable by changing its marker, never the other way round.

| Experimental | Why |
| --- | --- |
| Completion webhook (`jobFinished` callback: payload, `X-VDE-Event`, `X-VDE-Signature`, retry schedule) | Deliveries are held in memory and can be lost on restart; durable delivery may change the retry schedule and headers. No consumer uses it yet. |
| `document.url` in the JSON submission (`documents.fetch`) | The v1.0 security review covers the fetch (SSRF); what is fetched and how it is refused may narrow. |

### The contract's MAYs

| Contract | Implemented as | Decision |
| --- | --- | --- |
| §2, §9: add fields and error codes | `method_not_allowed` (`405`), `verification`, `attachments`, `content_deleted_at`, `judge_confidence` | stable |
| §3: finer scopes | `jobs:read` | stable |
| §4: client sends `Idempotency-Key` | Implemented as the contract's MUST for the service | stable |
| §4: HTML as visible text | Representation choice, not API; in `service_version` | not API |
| §5: `DELETE /v1/jobs/{id}` | `204`, content deleted at once | stable |
| §6: strip code fences and prose | Parsing, not API; framings are in `service_version` | not API |
| §7: `accept` carries entries | Advisory feedback on `accepted` | stable |
| §7: extra judge keys | `judge_confidence` (0 to 1 or `null`, advisory) | stable |
| §16: synchronous `POST /v1/extract` | The submit request, `200` terminal or `202` to poll | stable |
| §16: completion webhook | `jobFinished` callback | experimental |

## What is frozen

| Item | Promise |
| --- | --- |
| Paths and methods | `POST /v1/jobs`, `POST /v1/extract`, `GET` and `DELETE /v1/jobs/{id}`, `GET /v1/quota`, `GET /v1/openapi.yaml`, `GET /healthz`, `GET /metrics` stay. |
| Request | Multipart part names and the JSON field names, their media types, types, counts and accepted values. No new required part or field. Unknown JSON fields stay `400`. |
| Response fields | Every field keeps its name, type, nullability and meaning; required fields stay required. |
| Statuses | `queued`, `running`, `accepted`, `rejected`, `failed`, `extracted`, and their meanings: only `accepted` is acceptance; a terminal status never changes. |
| `verification` | `judge`, `grounded`, `none`; `null` exactly while the job is queued or running. |
| Feedback | `pointer` (RFC 6901 into `result`), `message`, `source` (`judge`, `schema`, `grounding`). |
| Error codes | Each code keeps its HTTP status and meaning (contract §9 and `method_not_allowed` `405`); the error body keeps its shape; messages never quote content. |
| Headers | `Authorization: Bearer`, `Idempotency-Key`, `X-Request-Id` (in and out), `Location`, `Idempotency-Replayed`, `X-Service-Version`, `X-Quota-Remaining`, `X-Quota-Reset`, `Retry-After` on every `429`, `Allow` on `405`, `Cache-Control: no-store`. |
| Scopes | `jobs` and `jobs:read` keep what they allow. |
| Idempotency | Per application, 24 hours, same key and body replays the job (content-deleted form included), different body `422 idempotency_mismatch`. |
| Retention | `none` and `default` keep their meaning; a terminal read of a `none` job deletes its content. |
| `service_version` | Present in every job and in `X-Service-Version`; two equal strings came from the same release, role configuration, framings and grounding rules. Treat it as opaque. |
| Limits | The 1.0 defaults are minimums for every later 1.x default: `document` 25 MiB, each `page` 10 MiB, 20 pages, each instructions part 64 KiB, `schema` 256 KiB, request 128 MiB. Over a limit is `413`. |

## v0.4 supersets

Frozen with the rest:

- The `judge` part and JSON field (`always`, `never`, `auto`), refused with
  `400 bad_request` whatever its value when the application does not allow override.
- The job field `verification` and the terminal status `extracted` (judge policy `never`,
  never acceptance).
- The feedback source `grounding`: its message is always `value not found in the
  document` and it never carries the value.
- The schema annotation `x-vde-grounding` with the values `critical`, `check`, `derived`
  and `skip`; any other value is `422 invalid_schema`. Schema validation ignores it.
- The default judge policy `always`, under which contract v1 holds exactly.

The grounding rules themselves (normalization, the date and time table, which leaves are
checked by default) are not part of the promise. They are versioned through
`service_version`, whose hash covers the rules version (`ground-2` at 1.0); a release that
changes them changes `service_version`, as a new model or framing would.

## What may change within `/v1`

- New paths, methods, optional request parts and fields, response fields, headers, error
  codes, statuses, `verification` values and feedback sources. Clients ignore fields they
  do not know, treat an unknown status as not accepted, an unknown `verification` as not
  `judge`, an unknown error code as a service failure, and an unknown source like
  `judge`.
- New accepted values in request enums, such as a document media type.
- Defaults the documentation flags as defaults (the retention period, `sync.max_wait`,
  `limits.job_timeout`, `judge.sample_rate`), announced in the CHANGELOG; the limits above
  never go below their 1.0 values. Operators set their own values for their deployment.
- Error message texts and the wording of `schema` feedback messages; judge feedback is
  model output.
- Results themselves: models, framings, representation choices (HTML as visible text,
  PDF text layer or pages) and the grounding rules change results, and each such change
  changes `service_version`.
- Which error a request with several faults answers, among the faults it has.
- Anything marked `experimental`.

## Deprecation

Something stable is never removed from `/v1`. It may be deprecated: marked `deprecated:
true` in the API document, listed under "Deprecated" in the CHANGELOG and in the API guide,
with what replaces it. It keeps working for as long as `/v1` is served.

## `/v2`

A change that cannot be additive (removing a field, changing a type or the meaning of a
status or code, a new required part) waits for `/v2`. `/v2` is served next to `/v1` by the
same binary; `/v1` stays available for at least 12 months after the first release that
serves `/v2`, and the release notes give the date it may go. A `/v2` needs an ADR first
(ADR 0005: the consumer's contract wins).

## Releasing

A 1.x release only grows `api/openapi.yaml`; the snapshot stays `api/openapi.v1.0.0.yaml`
for all of `/v1`. `info.version` is the API's version: the minor goes up with additions.
The service's own release is in `service_version`. `/v2` gets its own document and
snapshot.

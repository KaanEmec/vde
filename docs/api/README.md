# vde HTTP API

The API is described by [`api/openapi.yaml`](../../api/openapi.yaml) (OpenAPI 3.1), which
the running service also serves at `GET /v1/openapi.yaml`. It is a superset of
TravelInbox's [`extraction-service-api.yaml`](../../FromTravelApp/extraction-service-api.yaml)
(ADR 0005); the prose [contract](../../FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md) is
canonical for `/v1`. Tests in `internal/api` keep the document, the route table and the
error codes in step, and check the superset relation.

## Guides and examples

- [guide.md](guide.md): authentication, submit, poll, statuses and feedback, idempotency,
  quota, retention, errors and schema-less mode.
- [admin.md](admin.md): the admin API (`/admin/v1` on `admin.listen`, admin tokens),
  described by [`api/admin-openapi.yaml`](../../api/admin-openapi.yaml).
- [writing-instructions.md](writing-instructions.md): how framings and your instructions
  compose, and how to design schemas.
- [`examples/submit.sh`](examples/submit.sh): submit, poll and read with curl. Call the
  service from your server only; never ship its token in a browser or mobile app.

## Stability

`/v1` is frozen at 1.0.0: the [compatibility policy](../compatibility.md) says what is
promised, what may still be added and how deprecation and `/v2` work. Each operation in the
API document carries `x-stability: stable` or `experimental`; experimental today is the
completion webhook (the experimental document `url` was removed in 1.1). A test keeps `api/openapi.yaml` a
superset of the frozen [`api/openapi.v1.0.0.yaml`](../../api/openapi.v1.0.0.yaml).

## Routes

| Route | Auth | Purpose |
| --- | --- | --- |
| `POST /v1/jobs` | bearer | Submit a document (multipart). `202` with the job. |
| `GET /v1/jobs/{id}` | bearer | Poll a job. |
| `DELETE /v1/jobs/{id}` | bearer | Delete a job's content early. `204`. |
| `GET /v1/quota` | bearer | The application's quota as a body. |
| `GET /v1/openapi.yaml` | bearer | The API document. |
| `GET /healthz` | none | `200 {"status":"ok"}`, or `503` with `reasons`. |
| `GET /metrics` | network | Prometheus text format. |

## Conventions

- **Base path** `/v1`; changes within it are additive only
  ([compatibility policy](../compatibility.md)). JSON in UTF-8, timestamps RFC 3339 UTC.
  Clients ignore fields they do not know.
- **HTTPS** is required outside loopback. Either `vde serve` terminates TLS
  (`server.tls`), or `server.trusted_proxy: true` and every request must carry
  `X-Forwarded-Proto: https` (loopback included, since the proxy usually connects from
  there). Plain HTTP is accepted from loopback peers only, and `vde serve` refuses to bind
  a plain listener to a non-loopback address. A refused request answers `400 bad_request`.
- **`X-Request-Id`** is on every response: the client's value when it is 1 to 128 visible
  ASCII characters, else a fresh `r_<ULID>`. Error bodies repeat it as `request_id`.
- **`X-Service-Version`** is on every `/v1/jobs` response, errors included, and equals the
  job's `service_version`.
- **Errors** have one shape, `{"error": {"code", "message", "request_id"}}` with
  `Content-Type: application/json`. Codes and statuses follow contract §9; vde adds
  `method_not_allowed` (`405`, with `Allow`). Messages are fixed texts that never quote
  documents, instructions, schemas or model output. `429` always has `Retry-After`.
- **Quota headers** `X-Quota-Remaining` and `X-Quota-Reset` are on every authenticated
  response, errors included.
- **Schema-less jobs**: the `schema` part is optional at the service level; without it
  the judge alone decides acceptance.
- **Caching**: every response has `Cache-Control: no-store` and
  `X-Content-Type-Options: nosniff`.
- **Logs** carry one line per request with `method`, `route` (the pattern, never the
  path or query), `status`, `duration_ms` and `request_id`; never headers or bodies.

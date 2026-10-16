# Admin API

Run-time management of applications, tokens and jobs over HTTP, for operators. The
reference is [`api/admin-openapi.yaml`](../../api/admin-openapi.yaml); a test keeps it and
the routes in step. It is not part of the TravelInbox contract.

## Listener and tokens

- `vde serve` serves `/admin/v1` on `admin.listen` (default `127.0.0.1:8091`), a listener of
  its own; `admin: {listen: ""}` disables it. A non-loopback address needs `server.tls`
  (the admin listener serves the same certificate) or `server.insecure_http`; otherwise
  configuration validation refuses it. `server.trusted_proxy` does not apply here: reach the
  admin listener directly, not through the reverse proxy.
- Every request needs an admin token: `vde token create --admin --expires 30d [--label …]`.
  Admin tokens have no application and the single scope `admin`; they are created by the
  CLI only, listed with `vde token list --admin` and revoked like any token. An application
  token gets `403` on `/admin/v1`; an admin token gets `403` on `/v1`.
- 60 requests per minute per admin token (`429 rate_limited` with `Retry-After` beyond).
- Same middleware as `/v1`: request id, panic recovery, access log, HTTPS outside loopback.

## Routes

| Route | Does | CLI equivalent |
| --- | --- | --- |
| `GET /applications` | list applications | `vde app list` |
| `POST /applications` | create a dynamic application | none |
| `GET`, `PATCH /applications/{id}` | read; change name, limits, retention, HTML policy, enabled | none |
| `GET`, `POST /applications/{id}/tokens` | list; issue a token (shown once) | `vde token list`, `vde token create` |
| `GET /applications/{id}/quota` | the current window, not charged | none |
| `DELETE /tokens/{id}` | revoke an application or admin token | `vde token revoke` |
| `GET /jobs?app=&status=&since=&limit=` | job metadata, newest first | none |
| `GET`, `DELETE /jobs/{id}` | job metadata; force delete in any state | none |
| `GET /audit?app=&since=&kind=&limit=` | audit events, oldest first, last 24 h by default | `vde audit list` |

Job resources here are metadata only: status, attempts, service version, representation,
media type, size, pages, retention, error code, timestamps and the provider token usage
(`input_tokens`, `output_tokens`, every run of the job included). Never the result, feedback,
error message, instructions, schema, idempotency key or attachment names.

## Applications declared in YAML and created here

An application has a `source`:

- `config`: declared under `applications:` in the YAML. At every start `vde serve`
  rewrites all of its stored fields from the entry, defaults applied (`name`,
  `max_attempts`, `quota.limit`, `quota.window`, `rate.requests_per_minute`,
  `rate.submits_per_minute`, `rate.max_in_progress`, `retention.default`,
  `retention.force_none`, `documents.html`) and enables it: the YAML wins. A `PATCH`
  therefore lasts until the next restart. Removing the entry disables it at the next start.
  `retention.delete_blobs_at_terminal` exists only in the YAML.
- `admin`: created with `POST /applications`. It is stored as created or patched and kept
  across restarts; the YAML never disables it. Adding an entry with its id to the YAML
  turns it into a `config` application at the next start.

Absent or zero settings take the YAML defaults. Changes take effect on the next request:
the API and the workers read the stored row, and the application's `service_version` is
computed from it.

## Audit

Changes are audited with the fixed kinds, actor `admin:<token id>`: `app.enabled` (create,
or `enabled: true`), `app.disabled` (`enabled: false`), `token.created`, `token.revoked`,
`job.deleted` (reason `admin`). Other `PATCH` changes and all reads are only in the
access log, since the fixed set of kinds has no update event.

## The CLI

The operator commands (`vde app`, `vde token`, `vde audit`) work on the database directly
(SQLite in WAL mode next to the running service) and call the same store and token code
as these handlers. They have no HTTP mode in v0.2.

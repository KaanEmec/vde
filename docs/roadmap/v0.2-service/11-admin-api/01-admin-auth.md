# Job: Admin authentication and listener

Version: v0.2 · Epic: 11 Admin API · Status: done · Depends on: 02-*

## Goal

`/admin/v1/*` on a separate listener (or the main one, loopback-only by default), with
admin tokens distinct from application tokens and every action in the audit log.
Keep it as small as the CLI: no sessions, no UI, bearer tokens only.

## Tasks

- [x] `admin: {listen: "127.0.0.1:8091", token_env: VDE_ADMIN_TOKEN}` or admin tokens
      stored like application tokens with scope `admin` and created only by the CLI.
      (Chosen: stored tokens, `vde token create --admin`; `admin.listen` only.)
- [x] Same middleware chain; stricter rate limit; audit every request (every change; see
      deviations).
- [x] Refuse to start with the admin API on a non-loopback listener without TLS.

## Acceptance criteria

- Application tokens cannot reach admin routes (403); admin tokens cannot submit jobs.

## Out of scope

A web UI.

## Outcome

Done.

- Admin tokens are rows of `tokens` with `application_id` NULL and the single scope
  `admin` (migration `0005_admin`, which rebuilds the table to drop NOT NULL and adds a
  CHECK tying the two together). `vde token create --admin --expires … [--label]` creates
  them (`--admin` with `--app` or `--scope` is a usage error; `auth.Create` refuses the
  admin scope with an application or another scope); `vde token list --admin` lists them;
  `vde token revoke` and `DELETE /admin/v1/tokens/{id}` revoke them. `auth.Authenticate`
  returns a principal without an application for them; `auth.Permits` never lets the admin
  scope satisfy a `/v1` need (`""` now means "any application scope").
- Config `admin.listen` (default `127.0.0.1:8091`; empty disables). Validation refuses a
  non-host:port value, the address of `server.listen` or `metrics.listen`, and a
  non-loopback address without `server.tls` unless `server.insecure_http`. `runServe`
  opens the listener and `serve(ctx, cfg, ln, adminLn, …)` serves `api.NewAdmin(deps)` on
  it, with TLS from `server.tls` when set, and drains it at shutdown.
- `api.NewAdmin` is the `/v1` server type with the admin route table: same recovery,
  request id, HTTPS enforcement and access log, then `authenticate(auth.ScopeAdmin)` and
  a limit of 60 requests per minute per admin token (`bucketAdmin`). Application tokens get
  403 on admin routes, admin tokens 403 on `/v1` (without quota headers: no application).
- Tests: `internal/api` `TestAdminScopes`, `TestAdminRateLimit`; `internal/auth`
  `TestCreate`, `TestPermits`; `internal/config` `TestAdminListen`; `cmd/vde`
  `TestAdminEndToEnd`.

Deviations:

- "Audit every request": changes are audited with the fixed kinds (`token.created`,
  `token.revoked`, `app.enabled`, `app.disabled`, `job.deleted`), actor
  `admin:<token id>`; refused admin requests write `auth.failed` like `/v1`; reads are only
  in the access log (no `admin.request` kind: the set stays fixed).
- `server.trusted_proxy` does not apply to the admin listener: it is reached directly, so
  loopback peers may use plain HTTP even when the main listener sits behind a proxy.
- No `token_env` variant: one kind of credential, stored and revocable like the others.

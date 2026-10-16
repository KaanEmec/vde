# Job: Admin authentication and listener

Version: v0.2 · Epic: 11 Admin API · Status: planned · Depends on: 02-*

## Goal

`/admin/v1/*` on a separate listener (or the main one, loopback-only by default), with
admin tokens distinct from application tokens and every action in the audit log.
Keep it as small as the CLI: no sessions, no UI, bearer tokens only.

## Tasks

- [ ] `admin: {listen: "127.0.0.1:8091", token_env: VDE_ADMIN_TOKEN}` or admin tokens
      stored like application tokens with scope `admin` and created only by the CLI.
- [ ] Same middleware chain; stricter rate limit; audit every request.
- [ ] Refuse to start with the admin API on a non-loopback listener without TLS.

## Acceptance criteria

- Application tokens cannot reach admin routes (403); admin tokens cannot submit jobs.

## Out of scope

A web UI.

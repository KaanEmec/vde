# Job: Applications, tokens and jobs administration

Version: v0.2 · Epic: 11 Admin API · Status: planned · Depends on: 01, 02-01, 02-02

## Goal

Create, update, disable applications; create, list, revoke tokens; list and inspect jobs
(metadata only) and force-delete a job; read quota usage. Mirrors the CLI.

## Tasks

- [ ] Dynamic applications coexist with YAML-declared ones: YAML wins on restart for
      the keys it sets; dynamic ones persist in the store (decided in 02-01).
- [ ] Handlers reuse the store methods the CLI uses; the CLI may switch to calling the
      admin API when a URL is configured, so there is one code path.
- [ ] Job listing returns metadata only (no result or feedback) with filters by
      application, status, time; `DELETE` force-deletes content.
- [ ] OpenAPI document `api/admin-openapi.yaml`; docs.

## Acceptance criteria

- Everything the CLI can do is possible over the admin API and audited.

## Out of scope

Editing configuration (endpoints, groups) at run time: restart with a new file.

# Job: Applications, tokens and jobs administration

Version: v0.2 · Epic: 11 Admin API · Status: done · Depends on: 01, 02-01, 02-02

## Goal

Create, update, disable applications; create, list, revoke tokens; list and inspect jobs
(metadata only) and force-delete a job; read quota usage. Mirrors the CLI.

## Tasks

- [x] Dynamic applications coexist with YAML-declared ones: YAML wins on restart for
      the keys it sets; dynamic ones persist in the store (decided in 02-01).
- [x] Handlers reuse the store methods the CLI uses; the CLI may switch to calling the
      admin API when a URL is configured, so there is one code path. (The CLI stays local
      in v0.2; see deviations.)
- [x] Job listing returns metadata only (no result or feedback) with filters by
      application, status, time; `DELETE` force-deletes content.
- [x] OpenAPI document `api/admin-openapi.yaml`; docs (`docs/api/admin.md`).

## Acceptance criteria

- Everything the CLI can do is possible over the admin API and audited.

## Out of scope

Editing configuration (endpoints, groups) at run time: restart with a new file.

## Outcome

Done. `internal/api/admin.go`, routes under `/admin/v1`:
`GET/POST /applications`, `GET/PATCH /applications/{id}`,
`GET/POST /applications/{id}/tokens`, `GET /applications/{id}/quota`,
`DELETE /tokens/{id}`, `GET /jobs?app=&status=&since=&limit=`, `GET/DELETE /jobs/{id}`,
`GET /audit?app=&since=&kind=&limit=`.

- Applications carry `source` (`config` or `admin`, migration `0005_admin`).
  `UpsertApplication` (serve start) rewrites every field of a YAML entry, defaults
  applied (`name`, `max_attempts`, `quota.limit`, `quota.window`, `rate.*`,
  `retention.default`, `retention.force_none`, `documents.html`), enables it and marks it
  `config`: the YAML wins, including over a PATCH and over `enabled: false`.
  `DisableApplicationsNotIn` only disables `config` rows, so `admin` rows persist across
  restarts. New store methods `CreateApplication` (`ErrDuplicate` → 409) and
  `UpdateApplication`. Bodies are validated and defaulted by `config.CheckApplication`,
  the same rules as an `applications[]` entry; ids match `^[a-z0-9][a-z0-9._-]{0,63}$`.
- Shared code paths: tokens through `auth.Create` and `auth.ParseExpires` (moved from
  `cmd/vde`), `RevokeToken`, `ListTokens`, `ListApplications`, `ListAudit` with
  `audit.ParseSince`, the quota through the `/v1` quota code.
- Dynamic applications and PATCHes take effect at once everywhere: authentication reads
  the row per request; the worker now reads the row per job for the attempt limit and HTML
  policy (`worker.Pool.application`); `service_version` is computed from the row
  (`api.Deps.AppServiceVersion` replaces the static `ServiceVersions` map). The worker's
  lease-time `max_in_progress` map stays configuration-only; the submit-time ceiling,
  now queued plus running, bounds dynamic applications.
- Jobs: `job.Store.ListJobs(JobFilter, limit)` newest first; responses carry exactly
  id, application_id, status, attempts, max_attempts, service_version, representation,
  media_type, bytes, pages, retention, error_code and the timestamps. Force delete removes
  blobs then the row in any state and writes `job.deleted` reason `admin`.
- Tests: `internal/api` `TestAdminApplications`, `TestAdminTokens`, `TestAdminJobs`
  (asserts the exact key set and no sentinel content), `TestAdminAudit`,
  `TestAdminOpenAPIMatchesRoutes`; `internal/job` `TestAdminApplications`,
  `TestCountInProgressAndListJobs`; `cmd/vde` `TestAdminEndToEnd` (restart keeps the
  dynamic application).

Deviations:

- The CLI keeps working on the database directly; no HTTP mode in v0.2. Both call the
  same store and `auth` functions, which is the shared code path.
- A PATCH that changes settings but not `enabled` writes no audit event (the fixed kinds
  have no update event); it is in the access log.
- Admin tokens cannot be created over HTTP (CLI only, job 01); the admin API can revoke
  them.
- `409 conflict` is a new error code, used only by the admin API (duplicate id, token for
  a disabled application).

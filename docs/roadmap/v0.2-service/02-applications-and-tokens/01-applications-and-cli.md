# Job: Applications model and `vde app`

Version: v0.2 · Epic: 02 Applications and tokens · Status: done · Depends on: 03-01

## Goal

Applications as the unit of ownership, quota, limits and retention, defined in
configuration (declarative) and mirrored into the store, with a small CLI to list them.

## Context

Contract §3 (applications, one per environment), §6 (attempt limit per application),
§8 (quota and rate limit per application), §10 (retention per application, operator can
force `none`), §12 (`applications` in the YAML).

## Decision inside this job

Applications are declared in the YAML (`applications:` list) so a deployment is
reproducible from config and secrets alone; tokens are created at run time (next job).
The admin API (epic 11, decision 10) can also create applications at run time; YAML wins
on restart for the keys it sets, dynamic applications persist in the store.

## Tasks

- [x] Config: `applications: [{id, name, max_attempts, quota: {limit, window: P1D},
      rate: {requests_per_minute: 300, submits_per_minute: 30, max_in_progress: 20},
      retention: {default: 24h|none, force_none: false}, documents: {html: raw_html|visible_text}}]`
      (defaults shown, decision 16; quota default 1000 per `P1D`); validation
      (unique ids, `window` ISO 8601 duration, `max_attempts ≤ limits.max_attempts`
      optional cap).
- [x] At start, upsert applications into the `applications` table (so jobs and tokens
      reference a row); an application removed from config is marked disabled, its tokens
      stop working, its jobs remain until expiry.
- [x] `vde app list` prints id, name, limits, token count, enabled.
- [x] `service_version` per application (v0.1 job 05-07) uses the application's attempt
      limit.

## Acceptance criteria

- Config with two applications starts; removing one and restarting disables it (test).

## Out of scope

Creating applications over HTTP (epic 11).

## Outcome

Done. Defaults (decision 16) are applied by `config.Parse`; `Validate` checks unique ids,
`quota.window` with `config.ParseWindow` (ISO 8601 weeks, days, hours, minutes, seconds:
`P1D`, `PT1H`, `P7D`; years and months refused) and positive limits. `vde serve` opens
`server.data_dir/vde.db` (`config.Server.DatabasePath`), upserts every configured
application and disables the others (`cmd/vde/app.go`, `syncApplications`), auditing
`app.enabled` and `app.disabled` (actor `config`). `vde app list` prints id, name,
enabled, attempts, quota, rates, retention and active tokens.
`extract.ApplicationServiceVersion(cfg, app, build)` hashes the application's attempt limit
and HTML policy; `api.Deps.ServiceVersions` sends it as `X-Service-Version` on
authenticated `/v1/jobs` responses. Test: `cmd/vde` `TestAuthEndToEnd` restarts with one
application removed and sees it disabled and its token refused. Deviations:

- `max_attempts` above `limits.max_attempts` is a warning, not an error: the contract
  makes the per-application limit the operator's choice, so the cap is advisory.
- Applications reach the database only through `vde serve`; `vde token create` for an
  application `serve` has not seen yet fails with a hint instead of syncing from the CLI
  (a CLI run with another file must not disable applications).
- Also in this job: default `server.listen` is `127.0.0.1:8090`; `server.insecure_http`
  (plain HTTP from any peer, warning at start and in `config check`, refused with `tls`);
  `vde healthcheck`. The operator commands (`app`, `token`, `healthcheck`) decode the
  configuration without validating it (provider keys need not be set) and fall back to
  `/etc/vde/vde.yaml` when `--config` is absent and `vde.yaml` does not exist (the image).
  `examples/vde.dev.yaml` sets `data_dir: ./vde-data`.

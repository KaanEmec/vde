# Job: Applications model and `vde app`

Version: v0.2 · Epic: 02 Applications and tokens · Status: planned · Depends on: 03-01

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

- [ ] Config: `applications: [{id, name, max_attempts, quota: {limit, window: P1D},
      rate: {requests_per_minute: 300, submits_per_minute: 30, max_in_progress: 20},
      retention: {default: 24h|none, force_none: false}, documents: {html: raw_html|visible_text}}]`
      (defaults shown, decision 16; quota default 1000 per `P1D`); validation
      (unique ids, `window` ISO 8601 duration, `max_attempts ≤ limits.max_attempts`
      optional cap).
- [ ] At start, upsert applications into the `applications` table (so jobs and tokens
      reference a row); an application removed from config is marked disabled, its tokens
      stop working, its jobs remain until expiry.
- [ ] `vde app list` prints id, name, limits, token count, enabled.
- [ ] `service_version` per application (v0.1 job 05-07) uses the application's attempt
      limit.

## Acceptance criteria

- Config with two applications starts; removing one and restarting disables it (test).

## Out of scope

Creating applications over HTTP (epic 11).

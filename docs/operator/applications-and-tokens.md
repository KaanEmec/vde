# Applications and tokens

An **application** is one caller of the service (a backend, an environment) with its own
limits and retention. A **token** is a credential that belongs to one application. Callers
present it as `Authorization: Bearer <token>`. Tokens identify an application, never a
person, so browsers and mobile apps must not hold one; call the service from a backend.

Applications are declared in the configuration file. Tokens are created on the command line
against the database the service uses. An admin HTTP API for both is planned (v0.2 epic 11);
until it ships, the commands below are the only way, and they need access to the host (or
`docker exec`) and to the data directory.

## Declare an application

```yaml
applications:
  - id: bookings
    name: Booking importer
    max_attempts: 3
    quota:
      limit: 1000
      window: P1D
    rate:
      requests_per_minute: 300
      submits_per_minute: 30
      max_in_progress: 20
    retention:
      default: 24h
      force_none: false
      delete_blobs_at_terminal: false
```

Only `id` is required. Every other key has the default shown or listed below; a zero or
omitted value means the default. The full key table is in the
[configuration reference](configuration.md#applications-used-from-v02).

| Key | Default | What it limits |
| --- | --- | --- |
| `max_attempts` | `limits.max_attempts` (3) | Extractor attempts per job. A request can lower it, never raise it. Above `limits.max_attempts` is a warning, since each attempt costs model calls. |
| `quota.limit`, `quota.window` | 1000 per `P1D` | Submissions per window (an ISO 8601 duration: `P1D`, `PT1H`, `P7D`; years and months are refused). Spent: `429 quota_exhausted`. Polls and quota reads are free. |
| `rate.requests_per_minute` | 300 | Requests per minute across `/v1`. Over it: `429 rate_limited`. |
| `rate.submits_per_minute` | 30 | Job submissions per minute. |
| `rate.max_in_progress` | 20 | Jobs queued or running at once. |
| `retention.default` | `24h` | How long a `retention: default` job stays readable: a duration or `none`. |
| `retention.force_none` | `false` | Treat every job as `retention: none`, whatever the request says. |
| `retention.delete_blobs_at_terminal` | `false` | Delete the document and pages when the job is terminal instead of at expiry. |
| `documents.html` | `documents.html` | HTML representation for this application: `raw_html` or `visible_text`. |

What the retention keys delete, and when, is in [retention](retention.md). Rate limits are
kept in memory, per process, and reset at restart. These limits cap the cost of a leaked
token, so set them to what the consumer needs.

### Apply a change

The service mirrors the applications into the database **when `vde serve` starts**, and does
not reload the file while running. To add an application or change a limit: edit the file,
run `vde config check`, restart the service. At start:

- A new application is created and enabled.
- An existing one is updated with the file's values.
- An application no longer in the file is **disabled**: its tokens stop working at the next
  request and its jobs stay until they expire. Put it back in the file and restart to
  re-enable it; its tokens work again if they have not expired or been revoked.

Each change of state is written to the [audit log](operations.md#audit-log)
(`app.enabled`, `app.disabled`). `vde app list` shows what the database holds: id, name,
enabled, attempt limit, quota, rate limits, retention and the number of active tokens.

```
$ vde app list --config vde.yaml
ID        NAME              ENABLED  ATTEMPTS  QUOTA    REQ/MIN  SUBMITS/MIN  IN PROGRESS  RETENTION  TOKENS
bookings  Booking importer  true     3         1000/P1D 300      30           20           24h        2
```

## Token lifecycle

A token looks like `vde_<8-character id>_<secret>`. The id is public (it appears in
`vde token list` and in audit events); only a SHA-256 hash of the secret is stored, so a lost
token cannot be shown again: create another.

### Create

The application must be in the database, so add it to the configuration and start
`vde serve` once before creating its first token.

```
vde token create --app bookings --scope jobs --expires 2027-06-30 --label "importer prod" \
  --config /etc/vde/vde.yaml
```

| Flag | Meaning |
| --- | --- |
| `--app` | Application id (required). The application must be enabled. |
| `--scope` | Comma-separated scopes (required): `jobs`, `jobs:read`. |
| `--expires` | Required. A date (`2027-06-30`, midnight UTC), an RFC 3339 time, or a duration from now (`90d`, `2160h`). Must be in the future and at most `auth.max_token_ttl` away (default 730 days). |
| `--label` | Free text shown by `vde token list`, up to a short limit. |
| `--config` | Configuration file; see [install](install.md). |

The command prints the token on **stdout and nothing else**, so a script can capture it
(`TOKEN=$(vde token create ...)`); the id, scopes and expiry go to stderr. Store it in your
secret manager at once; it is not shown again.

### Scopes

| Scope | Allows |
| --- | --- |
| `jobs` | Submit a job, poll, delete a job, read the quota. |
| `jobs:read` | Poll and read the quota only. |

Give a poll-only client `jobs:read`. A request outside the token's scopes answers `403
forbidden`. Any valid token may fetch `GET /v1/openapi.yaml`.

### List

```
vde token list --app bookings
ID        SCOPES  STATUS  EXPIRES               LAST USED             CREATED               LABEL
a1b2c3d4  jobs    active  2027-06-30T00:00:00Z  2026-10-16T09:12:44Z  2026-10-16T08:00:00Z  importer prod
```

`STATUS` is `active`, `expired` or `revoked`. `LAST USED` is updated at most once a minute.
Use it to find tokens that are no longer used.

### Revoke

```
vde token revoke a1b2c3d4
```

Takes a token id (or the whole token; only its id is used). It takes effect on the very
next request: the service reads the token from the database on every request and caches
nothing. Revoking an already revoked token is a no-op. A running service sees changes made
by the commands without a restart.

### Rotate

Because an application may have several active tokens, rotation has no downtime:

1. Create the new token: `vde token create --app bookings --scope jobs --expires 2027-06-30`.
2. Deploy it to the caller and confirm it works (`GET /v1/quota` is a cheap check).
3. Check that the old token is no longer used: `vde token list --app bookings` and look at
   `LAST USED` of the old id.
4. Revoke the old token: `vde token revoke <old id>`.

If a token leaks, skip to step 4 after creating a replacement, and review the
[audit log](operations.md#audit-log) for `auth.failed` events and the metrics for unexpected
submissions. What a leaked token can do is bounded by its scopes, the application's quota
and its expiry; see [security](../security.md).

### Expiry

Every token expires; there are no permanent tokens. Choose the shortest lifetime your
rotation routine can live with, and lower `auth.max_token_ttl` to enforce a ceiling:

```yaml
auth:
  max_token_ttl: 2160h   # 90 days
```

The cap applies when a token is created; lowering it later does not shorten existing
tokens. An expired token answers `401`. The service removes revoked and expired tokens from
the database 90 days after they ended.

## What the caller sees

Every refusal of a token (missing, malformed, unknown, wrong secret, revoked, expired,
application disabled) answers the same `401 unauthorized` with `WWW-Authenticate`, so a
caller cannot tell which. The reason is recorded in the audit log as `auth.failed`
(`reason`: `missing`, `malformed`, `unknown_token`, `wrong_secret`, `revoked`, `expired`,
`application_disabled`); see [troubleshooting](troubleshooting.md#401-unauthorized).

## Admin API (planned)

Run-time management of applications and tokens over HTTP, on its own listener with its own
credentials, is planned as v0.2 epic 11. It will mirror the commands on this page, share
their code and be audited like them. Editing endpoints, groups and roles at run time is not
planned: edit the file and restart.

# Operator guide

How to install, configure, run, upgrade and troubleshoot the `vde` service without reading
the code. Callers of the HTTP API read the [API guide](../api/guide.md) instead.

## Start here

New deployment: [install](install.md) (binary, container or compose), then
[providers](providers.md) (pick models), then [applications and tokens](applications-and-tokens.md)
(let a caller in). The fastest path is the [compose example](../../examples/compose/README.md):
about ten minutes from clone to a first job.

## Pages

| Page | Read it to |
| --- | --- |
| [install.md](install.md) | Run `vde` from a release binary, the container image or compose; set up the data directory and TLS. |
| [configuration.md](configuration.md) | Look up a configuration key: type, default, meaning. Explains the example files. |
| [providers.md](providers.md) | Choose models per role, declare `accepts`, set up failover groups. |
| [applications-and-tokens.md](applications-and-tokens.md) | Declare applications and their limits; create, rotate and revoke tokens. |
| [operations.md](operations.md) | Health checks, metrics, audit log, logs, backups, upgrades. |
| [troubleshooting.md](troubleshooting.md) | Look up an error code, a `config check` message or a provider failure. |
| [retention.md](retention.md) | What is deleted, when, and what remains. |
| [privacy.md](privacy.md) | What is logged, what never is, the `debug.log_content` switch and data residency. |
| [storage.md](storage.md) | Choose SQLite or PostgreSQL and local files or S3; pool sizing, backups, bucket lifecycle rule, migrations. |
| [pdf.md](pdf.md) | Choose how PDFs reach models that do not read PDFs: text layer, rendered pages or caller pages; memory and limits. |
| [framings.md](framings.md) | The fixed prompts of the extractor and judge roles. |
| [evaluation.md](evaluation.md) | Measure extraction quality on a corpus with `vde eval`; build a corpus from production rejections. |

Related pages outside this folder: [provider adapters](../providers/README.md),
[security and privacy](../security.md) (threat model and the checklist before exposing the
service), [examples](../../examples/README.md) and the [changelog](../../CHANGELOG.md).

## The commands

`vde` is one binary. Run `vde <command> -h` for the flags of a command.

| Command | Purpose |
| --- | --- |
| `vde serve [--config path] [--role all\|api\|worker]` | Run the service: API, worker pool, expiry sweeper, `/healthz`, `/metrics`; `--role` splits API and workers into separate processes on PostgreSQL and S3 ([operations](operations.md#process-roles)). |
| `vde config check [--config path] [--strict] [--probe]` | Validate a configuration without network calls and print a summary. |
| `vde token create\|list\|revoke` | Manage access tokens. See [applications and tokens](applications-and-tokens.md). |
| `vde app list` | List the applications in the database. |
| `vde migrate [--config path]` | Apply pending database migrations of `storage.driver` and print the schema version (`serve` also migrates at start). See [storage](storage.md#migrations). |
| `vde healthcheck [--config path]` | Request `/healthz` on the configured listener; exit 0 on `200`. Used by the container `HEALTHCHECK`. |
| `vde extract` | Run one document through the extractor and judge from the command line; for trying a configuration. See [examples](../../examples/README.md#extract-a-document). |
| `vde eval` | Run a corpus with ground truth through the engine or a deployment and report false accepts, acceptance rate and field accuracy. See [evaluation](evaluation.md). |
| `vde version` | Print version, commit and build date. |

`--config` defaults to `vde.yaml`. The commands `app`, `token`, `audit`, `migrate` and `healthcheck` fall back
to `/etc/vde/vde.yaml` (the container path) when `--config` is not given and `vde.yaml` does
not exist, so `docker compose exec vde vde token ...` needs no flags. `vde serve` and
`vde config check` do not fall back.

Exit codes: 0 success, 1 usage error, configuration error or any other failure. `vde extract`
also uses 2 (rejected) and 3 (failed).

## What is planned

Not implemented yet; the guide marks it wherever it comes up:

- Provider reachability probes: `vde config check --probe` reports "probe not available"
  for every provider (planned for v0.3).
- The `debug.log_content` sink: `serve` opens the content log file, but nothing writes to it
  yet.

The admin HTTP API (`admin.listen`, see [applications-and-tokens.md](applications-and-tokens.md)
and [../api/admin.md](../api/admin.md)) and `vde audit list` are implemented.

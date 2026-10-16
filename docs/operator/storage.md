# Storage: SQLite or PostgreSQL, files or S3

Part of the [operator guide](README.md). `vde` keeps two kinds of state: **job rows**
(applications, tokens, jobs with their instructions and results, quota counters, audit
events) in a job store, and **blobs** (documents and page images) in a blob store. Each has
two drivers, chosen in the configuration ([keys](configuration.md#storage-and-blobs)):

| | Default | Alternative |
| --- | --- | --- |
| Job store, `storage.driver` | `sqlite`: `server.data_dir/vde.db` | `postgres`: a PostgreSQL 13+ database |
| Blob store, `blobs.driver` | `fs`: `server.data_dir/blobs/` | `s3`: a bucket on AWS S3, MinIO, Cloudflare R2, Backblaze B2 |

Callers see no difference: the API, the retention rules and the results are the same on
every combination.

## Which to choose

- **One host, one process: keep the defaults.** Nothing to run besides `vde`, backups are a
  file copy, and the quota of a first deployment (thousands of jobs a day) is far below what
  SQLite handles.
- **Several replicas** (for availability, or more workers than one host should run):
  `postgres` and `s3`, both. Replicas share the queue through the database (jobs are leased
  row by row, so two replicas never run the same job) and the documents through the bucket.
  `postgres` with `fs` works only when every replica mounts the same `blobs/` directory;
  `config check` warns about it.
- **One process, managed database**: `postgres` alone is fine when you want your provider's
  backups, point-in-time recovery and monitoring instead of a file on a volume.
- **Small or ephemeral disks**: `s3` alone moves the bulky part (documents) off the host.

Per-process limits stay per process by default: `rate.requests_per_minute` and
`rate.submits_per_minute` are token buckets in each replica's memory, so N replicas admit up
to N times the rate (a load balancer spreading requests evenly makes that roughly N times).
Set the top-level `rate.shared: true` to count them in the job store instead: one fixed
window per application and UTC minute, one `UPSERT` per request, one limit across all
replicas. A fixed window admits the whole limit at the end of one minute and again at the
start of the next, so a burst of up to twice the limit can pass within a few seconds; the
token bucket smooths that, per process. Quotas (`quota.limit`) are rows in the job store and
are shared exactly.
`rate.max_in_progress` is counted in the job store, but the count is not locked: replicas
admitting or leasing at the same instant may each count before the other commits, so an
application can briefly exceed it by the number of replicas.

How the processes split the work (`vde serve --role api|worker`) and what each reports on
`/healthz` is in [operations](operations.md#process-roles); a compose example with one API,
two workers, PostgreSQL and RustFS is [`examples/compose/scaled/`](../../examples/compose/scaled/README.md).

## PostgreSQL

Create a database and a role that owns it; `vde` creates its tables on first start.

```
CREATE ROLE vde LOGIN PASSWORD '…';
CREATE DATABASE vde OWNER vde;
```

```yaml
storage:
  driver: postgres
  dsn_env: VDE_DATABASE_URL   # the default; the variable holds the DSN
```

```
export VDE_DATABASE_URL='postgres://vde:…@db.internal:5432/vde?sslmode=verify-full'
```

The DSN is a `postgres://` URL or a `key=value` string, with every
[libpq parameter pgx understands](https://pkg.go.dev/github.com/jackc/pgx/v5/pgconn#ParseConfig)
(`sslmode`, `sslrootcert`, `connect_timeout`, `search_path`, …). It holds the password, so
it lives only in the environment: `config check` prints the variable's name, never its value,
and connection errors are redacted. Use `sslmode=verify-full` (or `require` on a private
network) whenever the database is on another host: job content crosses that connection.

### Pool settings

Each process opens at most `storage.max_open_conns` connections (default 16), keeps half of
them idle, closes idle ones after 5 minutes and recycles every connection after 30 minutes,
so a database failover or a PgBouncer restart heals without a restart of `vde`. A worker
holds a connection only for one statement at a time, so the pool needs roughly
`worker.size` plus a few for the API and the sweeper; fewer than `worker.size + 2` is a
warning. Size the server for all replicas: `replicas × max_open_conns` must stay below its
`max_connections`, with room for your tools.

Behind PgBouncer in transaction mode, add `default_query_exec_mode=exec` to the DSN: `vde`
otherwise caches named prepared statements per connection, which transaction pooling does
not preserve.

### What PostgreSQL keeps after a deletion

`retention: none` and expiry clear or delete rows as on SQLite, but PostgreSQL keeps the old
row versions until autovacuum reclaims them (minutes on a busy table), and the write-ahead
log, its archive and every backup keep them until those expire. Keep the WAL archive and
backup retention as short as your retention promises require, and encrypt the database's
storage. The SQLite checkpoint described in [retention](retention.md#what-is-on-disk) has no
PostgreSQL counterpart.

### Backups

Use what the database offers: a managed service's snapshots and point-in-time recovery, or
`pg_dump --format=custom vde > vde-$(date +%F).dump` from a host with the client tools. As with
SQLite, losing the database loses the tokens (recreate and redistribute them), unread
results and the audit log; applications are recreated from the configuration at the next
start. Restore into an empty database, start one replica, check `vde app list` and
`/healthz`, then start the others.

## S3-compatible blob store

```yaml
blobs:
  driver: s3
  bucket: vde-documents
  region: eu-central-1
  # endpoint: https://<account>.r2.cloudflarestorage.com   # R2, with region: auto
  # endpoint: http://127.0.0.1:9000                         # MinIO, with path_style: true
  # path_style: true
  # sse: AES256                                             # the default; "" turns it off
```

Credentials come from the AWS default chain: `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` in the environment, a profile, or the instance, task or pod role.
Create the bucket beforehand; `vde serve` checks at start that it can reach it.

Objects are `jobs/<job id>/document` and `jobs/<job id>/page-NN`, written whole (a reader
sees all of an object or none) and deleted with the job's content. The orphan sweep lists
`jobs/` and removes the objects of jobs that have no row and were not touched for an hour; it
lists at most 10,000 objects per sweep and continues where it stopped at the next one.

The service needs these permissions on the bucket and nothing else:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow", "Action": ["s3:ListBucket"], "Resource": "arn:aws:s3:::vde-documents"},
    {"Effect": "Allow", "Action": ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"],
     "Resource": "arn:aws:s3:::vde-documents/jobs/*"}
  ]
}
```

**Encryption.** `blobs.sse: AES256` (the default) asks the store to encrypt every object
with keys it manages (SSE-S3); AWS S3 encrypts new objects this way anyway. MinIO without a
KMS (and RustFS without a master key) refuses the header: set `sse: ""` there and encrypt the
disks instead.

**Versioning.** Leave versioning off on this bucket. With versioning, a deletion only adds a
delete marker and the document stays as a noncurrent version; if policy requires
versioning, add a lifecycle rule expiring noncurrent versions after one day.

### Bucket lifecycle rule

`vde` deletes blobs itself, but a lifecycle rule is a safety net for blobs it never got to
delete (a crashed replica, a bucket restored from elsewhere). Expire `jobs/` objects a day
after the longest time any job may keep its document:

- every application `retention: none` (or `force_none`): jobs live at most 24 hours, so
  **expire after 2 days**;
- otherwise: the longest `retention.default` of your applications, plus a day.

```
aws s3api put-bucket-lifecycle-configuration --bucket vde-documents --lifecycle-configuration '{
  "Rules": [{"ID": "vde-safety-net", "Status": "Enabled", "Filter": {"Prefix": "jobs/"},
             "Expiration": {"Days": 2}}]}'
mc ilm rule add --prefix jobs/ --expire-days 2 local/vde-documents   # MinIO
```

S3 lifecycle rules run about once a day, so they are a backstop, not the deletion schedule.

### Backups

Documents are short-lived inputs, not records: most deployments do not back up the bucket.
If you do, the copies inherit the retention promises; expire them on the same schedule.

### Other stores

Azure Blob Storage and Google Cloud Storage have no native driver. Use them through an
S3-compatible gateway that supports multi-object delete (`DeleteObjects`), which `vde` uses
to remove a job's blobs; the GCS XML interoperability API does not.

## Migrations

`vde serve`, and every operator command that opens the store, applies pending schema
migrations at start, each in its own transaction, recorded in `schema_migrations`. Both
drivers have the same numbered migrations, so a version number means the same schema on
either. To migrate explicitly, for example from a deploy job before replicas of a new
version start:

```
vde migrate --config /etc/vde/vde.yaml
storage postgres: schema version 7
```

Concurrent starts are safe: on PostgreSQL a migration takes an advisory lock, on SQLite the
write lock, so each migration is applied once. Downgrades are unsupported, as described in
[operations](operations.md#upgrades-and-migrations).

## Moving from SQLite to PostgreSQL

There is no converter: jobs are short-lived and applications come from the configuration.
Stop submissions and let running jobs finish, set `storage.driver: postgres`, start, and
create new tokens (`vde token create`) for every caller. Read any results you still need
before switching.

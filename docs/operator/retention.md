# Retention: what is deleted, when

Status: v0.2. The first-read deletion (the `GET` handler, epic 04 job 03) is specified
here and not yet wired; the other deletion points are implemented.

Every job has an effective retention, `none` or `default`, fixed when it is submitted.
This page says what each one deletes, at which moment, and what is left afterwards. The
promise to callers is in the contract, section 10; the caller-facing summary is in the
[API guide](../api/guide.md#retention).

## How a job's retention is decided

At submit the service resolves the retention against the application, in this order:

1. The request asks for `retention: none`: the job is `none`.
2. The application has `retention.force_none: true`: the job is `none`, whatever the
   request asked.
3. The application's `retention.default` is `none`: the job is `none`.
4. Otherwise the job is `default`.

The result is stored on the job row, together with `expires_at`:

| Effective retention | `expires_at` |
| --- | --- |
| `none` | `created_at` + 24 hours (the contract's upper bound) |
| `default` | `created_at` + the application's `retention.default` (24h unless you changed it) |

A job never outlives `expires_at`. Changing `retention.default` later does not move the
`expires_at` of jobs that already exist; setting `force_none` later does apply to them at
the terminal state (the worker checks the configuration).

## The matrix

Four moments delete things. "Inputs" are the document and page blobs plus the
instructions and the schema; "outputs" are the result, feedback, judge confidence and
error.

| Deletion point | Done by | `retention: none` | `retention: default` |
| --- | --- | --- | --- |
| Terminal state (`accepted`, `rejected`, `failed`) | worker (the sweeper, for a job whose worker was lost) | Inputs are deleted: blobs first, then the instruction and schema columns. Outputs stay for the read. | Nothing, unless the application sets `retention.delete_blobs_at_terminal: true` (or `force_none`): then inputs as for `none`. |
| First terminal `GET` | the `GET /v1/jobs/{id}` handler | After the body is returned, outputs are deleted in the same request and the job is marked content-deleted. A second `GET` is `404`. The row stays, without content, until `expires_at`. | Nothing: the job stays readable until `expires_at`. |
| `DELETE /v1/jobs/{id}` | the `DELETE` handler | Everything at once: blobs, content and the row. `204`. | Everything at once. |
| `expires_at` | the sweeper | Whatever is left: blobs and the row. | Blobs (if still there), content and the row. |

After the row is removed the job is `404`, for both policies. What remains of a job after
expiry is its audit events, which carry no content (see below).

### What each cell removes

| Thing | Where it lives | Deleted at terminal (`none`) | Deleted at first read (`none`) | Deleted at expiry or `DELETE` |
| --- | --- | --- | --- | --- |
| Document bytes and page images | blob directory | yes | already gone | yes |
| Extractor and judge instructions, schema | `jobs` row | yes | already gone | yes (the row) |
| Result, feedback, judge confidence, error | `jobs` row | no | yes | yes (the row) |
| Job metadata (application, media type, size, status, times) | `jobs` row | no | no | yes (the row) |
| Audit events | `audit_events` | no | no | no: see `audit.retention` |

Temporary files: blobs are written as a temporary file in the job's own directory and
renamed into place, so deleting the job's directory removes them too. There is no
response cache and no in-memory copy of a job: a `GET` after any deletion reads the
database and is `404`.

## What the sweeper does

The sweeper runs inside `vde serve` every `retention.sweep_interval` (default one
minute). Each sweep:

1. Removes expired jobs in batches of 100: the job's blob directory first, then the row.
   If the blobs cannot be removed the row stays, so the next sweep tries again; a job is
   never left with blobs and no row because of the sweeper.
2. Finishes jobs still `running` after `created_at` + `limits.job_timeout` + one minute
   as `failed` with error code `timeout` (their worker is gone), keeping the last
   candidate and feedback that were saved, and then applies the terminal-state deletion
   of the first row of the matrix.
3. Removes quota counters older than two quota windows, tokens revoked or expired more
   than 90 days ago, and audit events older than `audit.retention` (default 365 days).
4. At start and then once a day, removes blob directories that have no job row (for
   example after a crash between storing the document and creating the job). Directories
   changed within the last hour are left alone, so a submission in progress is safe.

An expired job is answered with `404` within one sweep interval of `expires_at`. The
sweeper does not shrink the SQLite file; to reclaim space, stop the service or run
`sqlite3 <data_dir>/vde.db 'VACUUM'` during a quiet moment.

## The audit record

Retention never deletes the audit record of a job early: it is the one thing that
outlives the content. It holds the application, job id, creation time, media type,
size, page count, terminal status, attempts, service version, error code, and whether
the job was deleted or expired, and never document bytes, instructions, schema, result
or feedback. Audit events are removed by the sweeper after `audit.retention`.

## Choosing a policy

- Sensitive documents, caller wants no copy: ask for `retention: none`, or set
  `retention.force_none: true` for the application so no caller can opt out.
- Callers that poll late or retry reads: use `default`, and consider
  `retention.delete_blobs_at_terminal: true` when the result is enough and the document
  should not stay on disk.
- To shorten what `default` keeps, lower `retention.default` (for example `1h`).

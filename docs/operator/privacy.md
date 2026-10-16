# Privacy: logging and the debug switch

Status: logging part for v0.2 (epic 06 job 03). Retention policies are described in the
retention guide.

`vde` is built so that what you extract from stays out of what you operate with. Logs,
metrics and error messages describe *that* something happened, never *what was in the
document*. This page says what is logged, what never is, and the one switch that
changes that.

## What is logged

Routine logs go to stderr as JSON (`logging.format: json`, the default) or text, at
`logging.level` (default `info`). Every line is built from a fixed vocabulary of keys:

| Group | Keys |
| --- | --- |
| Standard | `time`, `level`, `msg`, `source` |
| Correlation | `job_id`, `app_id` (also `application_id`), `request_id`, `worker_id` |
| Job state | `status`, `attempts`, `error_code`, `outcome`, `kind`, `role`, `count` |
| Routing | `endpoint`, `model` (configuration ids) |
| Request and document metadata | `method`, `route`, `media_type`, `bytes`, `pages`, `duration_ms` |
| Process | `service_version`, `version`, `component`, `listen`, `tls`, `trusted_proxy`, `drain_timeout` |
| Diagnostics | `error`, `reason`, `panic`, `stack` |

The vocabulary is enforced, not just followed: the logger drops any attribute whose key
is not on this list, and cuts string values longer than 256 characters (stack traces
4,096). A future coding mistake, such as logging a request body under a new key, produces
no output rather than a leak. The list is `logging.AllowedKeys` in
`internal/logging`; a test pins it.

## What is never logged

At no level, including `debug`:

- document bytes, page images, extracted text, email bodies and attachments;
- the caller's extractor and judge instructions, and the schema;
- candidates (the extractor's answers), the judge's feedback and verdicts;
- provider request and response bodies, and provider error bodies (the service logs the
  HTTP status and the provider's error code only);
- API keys, bearer tokens and other credentials, and caller-supplied file names.

The same rule covers metrics, whose labels are configuration ids only, and error
messages returned by the API, which never quote content. A test in `internal/logging`
runs the extraction loop with a unique marker planted in the document, both
instructions, the schema and the model answers, and fails if any marker appears in a log
line.

One residual risk to know about: the `error` value of a failed provider call is the
adapter's message. Adapters are required not to put response bodies in it; if you write
a custom provider, keep to that rule.

## The debug switch: `debug.log_content`

Setting `debug.log_content: true` records content (documents, instructions, candidates,
provider bodies) for local debugging. It is **unsafe** and off by default.

- It is refused by `vde config check` and `vde serve` unless `server.listen` is a
  loopback address (`127.0.0.1`, `[::1]` or `localhost`), so it cannot run on a public or
  shared listener.
- The service logs a warning at startup when it is on.
- Content goes to a separate file, created with mode `0600` in the data directory, never
  to stdout or stderr and never through the routine logger. The file refuses `/dev/stdout`
  and the other standard streams.
- Treat that file like the documents themselves: it has no retention schedule, so delete
  it when you finish, and never ship it to a log aggregator.

Leave the switch off everywhere that handles real documents.

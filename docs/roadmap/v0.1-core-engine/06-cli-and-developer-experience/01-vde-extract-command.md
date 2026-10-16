# Job: `vde extract` command

Version: v0.1 · Epic: 06 CLI and developer experience · Status: done · Depends on: 05-05, 05-06

## Goal

Run one extraction from the command line with the same code path the service will use,
printing the job-shaped result as JSON.

## Context

Lets the engine ship and be demonstrated before the HTTP service exists, and remains the
fastest way for a caller to iterate on instructions and schemas (TravelInbox's prompt
authors can use it locally).

## Tasks

- [x] `vde extract --config vde.yaml --document file (any accepted type, including .eml) --type media/type --page p1.png
      --page p2.png --extractor-instructions file --judge-instructions file
      [--schema file] [--max-attempts n] [--html raw_html|visible_text] [--timeout d]`.
      Media type defaults from the file extension when `--type` is absent.
- [x] Output: a JSON object with the fields of the contract's job resource that apply
      (`status`, `result`, `feedback`, `attempts`, `service_version`, `error`,
      `finished_at`), to stdout; progress lines (attempt n, extractor endpoint used,
      verdict) to stderr with `--verbose`; never document content on stderr.
- [x] Exit codes: 0 accepted, 2 rejected, 3 failed, 1 usage or configuration error.
- [x] `--provider-script file` switches every endpoint to the fake provider driven by
      the script (for scenario tests and demos without keys).

## Acceptance criteria

- The exit-criterion documents of the version README (text, HTML, PDF with pages, image)
  run through the command with the dev configuration.

## Out of scope

Batch processing of folders (a shell loop does it), a TUI.

## Notes

Implemented in `cmd/vde/extract.go` (command), `cmd/vde/providers.go` (the provider
factory: `anthropic`, `openai_compatible`, `gemini`; `fake` only with `--provider-script`;
plus a wrapper that reports each call for `--verbose` and counts tokens).

- The exit codes of every command now follow this job: 1 is a usage or configuration error
  everywhere (`vde config check` and the dispatcher used 2 for usage before).
- `--max-attempts` may only lower `limits.max_attempts` (as a request may in the API);
  `--timeout` replaces `limits.job_timeout` for the run.
- `--provider-script` takes a fake script or a `testdata/scenarios` file (the fake reads
  the first YAML document only). One fake serves every endpoint, so steps are consumed in
  call order across roles and matched by role or model. Keys named by the configuration
  need not be set then, since no endpoint is called.
- For an `.eml` the output lists `attachments` (`name`, `media_type`, `bytes`), which
  closes the open task of epic 04 job 04.
- `service_version` is logged with slog at start (level info), printed by
  `vde config check` and returned in the output.
- Verified with the dev configuration and `--provider-script`: text, HTML, `.eml`, PDF
  with its page and JPEG, with and without the schema, all `accepted` (exit 0);
  `three-rejections.yaml` exits 2. A run against a real key is job 04's.

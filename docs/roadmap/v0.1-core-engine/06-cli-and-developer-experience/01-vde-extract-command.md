# Job: `vde extract` command

Version: v0.1 · Epic: 06 CLI and developer experience · Status: planned · Depends on: 05-05, 05-06

## Goal

Run one extraction from the command line with the same code path the service will use,
printing the job-shaped result as JSON.

## Context

Lets the engine ship and be demonstrated before the HTTP service exists, and remains the
fastest way for a caller to iterate on instructions and schemas (TravelInbox's prompt
authors can use it locally).

## Tasks

- [ ] `vde extract --config vde.yaml --document file (any accepted type, including .eml) --type media/type --page p1.png
      --page p2.png --extractor-instructions file --judge-instructions file
      [--schema file] [--max-attempts n] [--html raw_html|visible_text] [--timeout d]`.
      Media type defaults from the file extension when `--type` is absent.
- [ ] Output: a JSON object with the fields of the contract's job resource that apply
      (`status`, `result`, `feedback`, `attempts`, `service_version`, `error`,
      `finished_at`), to stdout; progress lines (attempt n, extractor endpoint used,
      verdict) to stderr with `--verbose`; never document content on stderr.
- [ ] Exit codes: 0 accepted, 2 rejected, 3 failed, 1 usage or configuration error.
- [ ] `--provider-script file` switches every endpoint to the fake provider driven by
      the script (for scenario tests and demos without keys).

## Acceptance criteria

- The exit-criterion documents of the version README (text, HTML, PDF with pages, image)
  run through the command with the dev configuration.

## Out of scope

Batch processing of folders (a shell loop does it), a TUI.

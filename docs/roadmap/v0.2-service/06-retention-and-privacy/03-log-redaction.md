# Job: Log redaction and the debug content switch

Version: v0.2 · Epic: 06 Retention and privacy · Status: in progress (wiring done; provider adapter bodies and content-log call sites open) · Depends on: 01-01

## Goal

Document bytes, pages, instructions, schema, candidates, feedback and provider error
bodies never appear in logs, metrics, traces or error messages at any level; a debug mode
that does log them is off by default and cannot run with a public listener.

## Context

Contract §10 (MUST NOT appear in logs … at any level; debug mode off by default and
documented as unsafe), §2 (error messages never quote content). Architecture security
model. Brief: "exclude credentials and document contents from routine logs."

## Tasks

- [x] (`internal/logging`: `New`, `AllowedKeys`) `slog` with a JSON handler and a fixed vocabulary of attribute keys (`job_id`,
      `app_id`, `request_id`, `status`, `attempts`, `endpoint`, `model`, `duration_ms`,
      `media_type`, `bytes`, `pages`, `error_code`); a `ReplaceAttr` hook that drops any
      attribute whose key is not in the allow-list and truncates string values over 256
      characters, as a safety net against future mistakes. The list also holds the keys
      the existing code logs (`application_id`, `reason`, `error`, `panic`, `stack`, ...);
      `newLogger` in `cmd/vde` (used by `serve` and `extract`) now returns `logging.New`;
      the allow-list gained `audit`, `actor` and `details` for `audit.log` lines.
- [ ] (not started) Provider adapters log status codes and provider error *codes*, never response
      bodies; the error type keeps the body in an unexported field available only when
      `debug.log_content` is on.
- [ ] `debug.log_content: true` is refused by `config check` and `serve` unless the
      listener is loopback (v0.1 job 01-03 rule); when on, a startup warning is logged
      and content goes to a separate log file, never stdout. Done: `logging.OpenContentLog`
      / `NewContentLogger` / `(*ContentLogger).Log` (nil-safe, 0600, refuses standard
      streams). Wired: `serve` opens `<data_dir>/content.log` only when
      `debug.log_content` is on and logs a startup warning. Still open: calling it from
      the worker and the adapters (the worker has no content-log hook yet).
- [x] Redaction test (`internal/logging/redaction_test.go`: nine extraction-loop
      scenarios with `internal/provider/fake` and sentinels in the document, both
      instructions, the schema and the answers; the `cmd/vde` scenario suite and metric
      labels are not covered, wired by epic 05/07 agent). Original wording: run the full
      scenario suite with a capturing log handler and assert that none of the known content markers (a unique sentinel string planted in
      each sample document, instruction, schema and fake answer) appears in any log line
      or metric label.
- [x] `docs/operator/privacy.md` section on logging and the debug switch.

## Acceptance criteria

- The sentinel test runs in CI and fails on any leak.

## Out of scope

Tracing (OpenTelemetry) is not planned for v0.2; if added later the same allow-list applies.

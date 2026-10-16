# Job: Configuration model

Version: v0.1 · Epic: 01 Project foundation · Status: done · Depends on: 01

## Goal

One YAML file, parsed into Go structs in `internal/config`, with secrets taken from
environment variables named in the file and defaults applied in one place.

## Context

Brief, "AI endpoints, roles, and failover" (illustrative YAML). Contract §12
("one YAML file plus environment variables for secrets ... `endpoints`, `groups`, `roles`,
`max_attempts`, plus `applications`"). Keep the shape close to the brief so operators
recognize it. Application-level settings are defined here but only used from v0.2.

## Proposed shape

```yaml
server:                      # used from v0.2
  listen: ":8090"
  tls: {cert_file: "", key_file: ""}          # or
  trusted_proxy: false                          # require X-Forwarded-Proto: https
  data_dir: "/var/lib/vde"

endpoints:
  gemini:
    provider: gemini
    base_url: "https://generativelanguage.googleapis.com"
    api_key_env: GEMINI_API_KEY
    models:
      gemini-model: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
  openrouter:
    provider: openai_compatible
    preset: openrouter                    # sets base URL and attribution headers
    api_key_env: OPENROUTER_API_KEY
    models:
      vendor/model: {accepts: [image/png, image/jpeg, text/plain]}
  anthropic:
    provider: anthropic
    base_url: "https://api.anthropic.com"
    api_key_env: ANTHROPIC_API_KEY
    timeout: 90s
    models:
      claude-fable-5-1: {accepts: [application/pdf, image/png, image/jpeg, image/webp, text/plain]}
  local:
    provider: openai_compatible
    base_url: "http://ollama:11434/v1"
    api_key_env: ""                       # optional for local endpoints
    models:
      some-vision-model: {accepts: [image/png, image/jpeg, text/plain]}

groups:
  extraction:
    endpoints:
      - {endpoint: anthropic, model: claude-fable-5-1}
      - {endpoint: local, model: some-vision-model}
    failover: {max_tries: 3, max_total_time: 3m}
  review:
    endpoints:
      - {endpoint: anthropic, model: claude-fable-5-1}

roles:
  extractor: {group: extraction}
  judge: {group: review}

limits:
  max_attempts: 3            # default per application, request can only lower it
  job_timeout: 5m            # hard max 8m
  judge_reparse_tries: 1

documents:
  html: raw_html             # default; or visible_text (per application override allowed)

applications: []             # v0.2: id, name, quota, rate limits, attempt limit, retention

logging: {level: info, format: json}
debug: {log_content: false}
```

## Tasks

- [x] Define the structs with yaml tags; keep durations as `time.Duration` parsed from
      strings; keep media types as strings validated later.
- [x] Load: read file, expand `${VAR}` only inside values that explicitly reference env
      (`api_key_env` pattern, no implicit interpolation elsewhere), apply defaults.
- [x] Resolve secrets at load into an unexported field so they never serialize; `String()`
      and any dump redact them.
- [x] Unit tests: full example parses; missing file, bad YAML and unknown keys fail with
      a path to the offending key (`yaml.v3` `KnownFields(true)`).
- [x] Document every key in `docs/operator/configuration.md` (table: key, type, default,
      meaning). Placeholder file created here, finished in v0.2 epic 10.

## Acceptance criteria

- The example above loads and round-trips in a test; unknown keys are rejected.
- No secret value appears in any `String()`, log or error output (test asserts this).

## Out of scope

Semantic validation (next job), hot reload (not planned; restart to reconfigure).

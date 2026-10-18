# Job: Configuration schema freeze and migration tooling

Version: v1.0 · Epic: 01 API freeze · Status: done · Depends on: 01

## Goal

Operators upgrading within 1.x never have to rewrite their configuration by hand.

## Tasks

- [x] Publish a JSON Schema for `vde.yaml` for editor validation; `config check` validates
      against it as well. Done as `internal/config/schema.json` (draft 2020-12, written by
      hand, `additionalProperties: false` on every object), embedded and printed by
      `vde config schema`; `config check` reports each violation as
      `error: schema: <key>: <message>` after its own checks. `serve` and `extract` do not
      read it: Go validation stays authoritative. `schema_test.go` keeps it in step with
      `config.Config` (both directions) and with the key tables of
      [configuration.md](../../../operator/configuration.md#schema).
- [x] Rename or restructure keys now if any are awkward (last chance), with a `vde config
      migrate` command that rewrites a 0.x file. No renames needed: the current keys are
      the frozen 1.x schema; migrate command not built.
- [x] Deprecation rule: old keys keep working for one minor version with a warning. Written
      as policy in [configuration.md, "Schema"](../../../operator/configuration.md#schema)
      and [compatibility.md, "Configuration"](../../../compatibility.md#configuration): the
      warning comes from `config check` and `serve`, then the key is removed; a new key
      arrives with a default that reproduces the old behaviour. No key is deprecated in 1.0.
- [x] The schema covers the v0.4 keys: `roles.judge.policy`, `applications[].judge.*`
      (`policy`, `allow_override`, `sample_rate`), `documents.pdf.*` (`mode`, `dpi`,
      `max_pages`, `min_text_chars_per_page`, `workers`) and
      `applications[].documents.pdf.mode`; also `limits.max_queued_jobs` (v1.0).

## Acceptance criteria

- Every example file validates against the schema; migration from the v0.2 example passes.
  Met: `TestExamplesMatchSchema` validates `examples/` (compose and scaled included),
  `testdata/scenarios/config/` and `internal/config/testdata/`; with no renames the v0.2
  example needs no migration and validates unchanged.

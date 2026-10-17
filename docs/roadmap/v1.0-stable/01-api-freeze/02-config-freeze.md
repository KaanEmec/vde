# Job: Configuration schema freeze and migration tooling

Version: v1.0 · Epic: 01 API freeze · Status: planned · Depends on: 01

## Goal

Operators upgrading within 1.x never have to rewrite their configuration by hand.

## Tasks

- [ ] Publish a JSON Schema for `vde.yaml` (`config/schema.json`) for editor validation;
      `config check` validates against it as well.
- [ ] Rename or restructure keys now if any are awkward (last chance), with a `vde config
      migrate` command that rewrites a 0.x file.
- [ ] Deprecation rule: old keys keep working for one minor version with a warning.
- [ ] The schema covers the v0.4 keys: `roles.judge.policy`, `applications[].judge.*`
      (`policy`, `allow_override`, `sample_rate`), `documents.pdf.*` (`mode`, `dpi`,
      `max_pages`, `min_text_chars_per_page`, `workers`) and
      `applications[].documents.pdf.mode`.

## Acceptance criteria

- Every example file validates against the schema; migration from the v0.2 example passes.

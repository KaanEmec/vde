# Job: Unused fields and outputs

Version: v1.1 · Epic: 01 Dead code · Status: planned · Depends on: 01-01

## Goal

Struct fields, options and results that are computed or set but never read in production
are gone. Structure only; requests on the wire and every result stay byte-identical.

## Context

Audit findings (`78b6486`):

- `internal/provider/provider.go:43` `Response.Model`: set by all seven adapters, read by
  nothing outside `internal/provider` (metrics use `router.Served.Model`). About 12 lines
  plus test assertions.
- `provider.Request.Temperature`: never set, always 0. OpenAI and Gemini must keep
  sending `0` explicitly so the wire does not change. About 15 lines.
- The `defaultMaxTokens` fallback in each adapter (`anthropic.go:168`,
  `openai/client.go:148`, `gemini.go:144`, `bedrock.go:91`, `vertex/claude.go:70`,
  `vertex/gemini.go:58`) is unreachable: `router.go:200` always sets `MaxTokens` from
  `Model.OutputTokens()`. About 20 lines. Keep a single guard in the router if the CLI or
  a test can bypass it (check first).
- `internal/ground`: `Kind`, `kindNames`, `Kind.String`, `Miss.Kind` and the kind returns
  of `checkLeaf`, `match`, `matchTemporal` are computed and then dropped by
  `extract.go:402`. `Result.Checked`, `Skipped`, `Missed` are never read;
  `Options.MinLength` and `DefaultMinLength` are never set by callers (`Options` can
  become one `maxMisses int`); the `ground.Feedback` type and `Misses.Feedback()` exist
  only to be converted into `extract.Feedback`. About 70 production lines.
  `RulesVersion` stays `ground-2`: matching does not change.
- `internal/eval/run.go:10-31` copies the status, verification and decision constants of
  `internal/extract`; `report.go:90-92` `Summary.CostUSD` is always null.
  `docs/operator/evaluation.md` documents `cost_usd`, and the three reports in
  `docs/quality/` contain it: check whether `--compare` decodes old reports strictly
  before removing the field.

## Tasks

- [ ] Confirm each field is unread in production.
  - Skill: EVID.
  - Prompt: "For Response.Model, Request.Temperature, each adapter's defaultMaxTokens,
    ground.Kind, Miss.Kind, Result.Checked/Skipped/Missed, Options.MinLength,
    ground.Feedback, eval Summary.CostUSD and the eval status constants: list production
    reads and writes with `gopls references`, and whether `router.go` always sets
    MaxTokens before an adapter runs (including the `vde extract` path). Report only."
- [ ] Provider fields.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/provider — remove Response.Model, Request.Temperature and
    the unreachable defaultMaxTokens fallbacks as reported; keep every request body
    byte-identical (OpenAI and Gemini still send temperature 0); update the httptest
    assertions. Guardrails: skills.md."
- [ ] Ground outputs.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode, then Execute mode: in internal/ground remove
    Kind and every kind return value, the unused Result counters, Options.MinLength and
    ground.Feedback; let internal/extract build its feedback from Miss.Pointer. RulesVersion
    and every grounding decision stay the same; run `make scenarios` and `make eval` with
    `--compare` against eval-reports/lean-baseline. Guardrails: skills.md."
- [ ] Eval duplicates.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify internal/eval — use internal/extract's status, verification and
    decision constants instead of the copies in run.go; remove Summary.CostUSD only if
    `--compare` still reads the reports in docs/quality and eval-reports/lean-baseline,
    and update docs/operator/evaluation.md. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`.

## Acceptance criteria

- Every provider httptest request-body assertion is unchanged; `make eval` numbers equal
  the baseline under all three policies; `make check` green.

## Out of scope

- The double timeout (`NewHTTPClient(ep.Timeout)` and the router's context timeout): kept
  as a safety net for paths that bypass the router.

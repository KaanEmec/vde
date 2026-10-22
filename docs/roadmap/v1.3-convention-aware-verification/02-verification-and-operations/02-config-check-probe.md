# Job: `vde config check --probe`

Version: v1.3 · Epic: 02 Verification and operations · Status: planned · Depends on: none

## Goal

Let an operator check, before production, that every configured model answers in its role
in a form vde can parse: one small synthetic request per endpoint-model pair and role, a
table of the results, a non-zero exit when any pair fails.

## Context

TravelInbox configured Mistral Small 4 as judge: every job sent to it ended `failed` or
`rejected`, and nobody could see why. A probe would have shown it in one call before any
document was sent. Contract §12 allows `config check` "an optional endpoint probe". v1.1
(decision L1) removed a `--probe` flag that was a never-implemented stub; this job
implements it for real.

The probe uses the real path: the role's framing, the request assembly, the adapter,
router failover off (each pair on its own) and the real parsers (`parseJSON` with the
`<json>` block for the extractor, `parseVerdict` with the `<verdict>` block for the judge).
The input is a tiny synthetic document and schema embedded in the binary, the same for
every pair, never a caller document. It never runs as part of `vde serve`.

## Tasks

- [ ] `cmd/vde`: `vde config check --probe` after the offline validation passes; for each
      role and each endpoint-model pair of the role's groups (and escalation steps), one
      request with the embedded sample; bounded by the endpoint's timeout and one try.
- [ ] Output, one line per pair and role: `ok`, `unparseable` (the parser's error, which
      names a position and no content), or the provider error class; latency, input and
      output tokens. Exit non-zero when any pair is not `ok`. No answer text is printed.
- [ ] The judge probe sends a candidate with one deliberate error, so `ok` also means the
      model rejected it with a feedback entry at that pointer; a model that accepts it is
      reported as `accepts a wrong candidate`, a warning, not a failure.
- [ ] Tests with the fake provider: an ok pair, an unparseable answer, a provider error,
      a judge that accepts the wrong candidate; nothing printed beyond the table.
- [ ] Docs: [operations](../../../operator/operations.md) and the `config check` usage;
      a short "Checking a model before production" paragraph in
      [docs/providers/README.md](../../../providers/README.md).
- [ ] Diagnose TravelInbox's Mistral Small 4 judge failure with the probe through
      OpenRouter, at most three requests, if the owner provides a key for it; record the
      cause in `docs/providers/` and the answer for epic 01 job 03's hand-over. Without a
      key, record that the probe is the tool TravelInbox can run themselves.

## Acceptance criteria

- `vde config check` without `--probe` makes no network call (test).
- The fake-provider tests above pass; output never contains the probe's answer text.
- Docs and the usage text agree (docs tests).

## Out of scope

Periodic probing from `vde serve`, health checks based on model answers, a list of
certified models.

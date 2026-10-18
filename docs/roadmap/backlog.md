# Backlog

Ideas that are deliberately not in v0.1 to v1.0. Listed so they are not forgotten and so
nothing in the first releases accidentally designs them in or out. Promote an item by
giving it an epic folder in a version and removing it here.

| Item | Why it waits | What would trigger it |
| --- | --- | --- |
| Review UI for rejected and failed jobs | Brief: outside the first release. TravelInbox has its own review flow. | A consumer without its own review surface |
| Temporal or another workflow engine | The bounded loop fits in one process (ADR 0003). | Workflows that outlive a process or span many steps |
| Sensitive-data masking or tokenization inside documents | Brief: a separate feature to consider. Needs a consumer's requirements. | A consumer whose provider policy forbids raw PII |
| OCR pipeline as a fallback for models without vision | Brief: no OCR planned initially. Operators choose vision- or PDF-capable models (ADR 0006). | A text-only provider that must be supported |
| Domain-specific schemas or database integrations | The service is domain-agnostic by contract. | Never inside this repository; example schemas may live under `examples/` |
| Multi-document jobs (one job, many files, one result) | Callers can split; TravelInbox does. | A consumer with cross-document extraction needs |
| Streaming partial results | Polling fits the first consumer. | A latency-sensitive interactive client |
| Judge-only endpoint (verify a caller-supplied candidate) | Not requested; small to add later behind the same loop code. | A consumer that extracts elsewhere |
| Per-user (not per-application) tokens with a sign-in flow | Brief mentions Swift and browser clients; contract says only server workers hold tokens. | A direct mobile or browser consumer |
| Cost accounting per application (tokens, money) | Usage is exposed: `vde_provider_tokens_total` by role, group, endpoint and model, and `input_tokens`/`output_tokens` per job (admin API, `job.finished` audit events). Money and per-application billing are an operator concern. | An operator hosting the service for several tenants |
| ~~Server-side PDF page rendering~~ | Resolved in v0.4 by [ADR 0010](../architecture/decisions/0010-pdf-preprocessing-in-service.md) (2026-10-17): the service reads the text layer and renders pages itself with pdfium on WebAssembly (`documents.pdf`). ADR 0006 had decided against it on 2026-10-16. | Done |
| Grounding of partially printed dates | In the offline fake-provider validation of the owner suite under `auto` (2026-10-18), the only critical values that sent emails and PDFs to the judge were derived dates: a next-day arrival, and dates printed without a year whose year comes from the document's own date. Today such a date is ungrounded and the judge decides, which is the safe default. | Measured judge cost on date-derived cases high enough to justify a rule "day and month printed, year equals the document date's year" in `internal/ground` |
| Helm chart | First deployment is docker compose on one VM. | A Kubernetes user |

## Deferred from v1.0 (owner's decision, 2026-10-18)

Drafts for all three are on the unpushed branch `wip/release-engineering` (one commit on
top of `691e651`); none has run in CI.

- **Load test and capacity document** (v1.0 03-01): a `test/load` Go tool (multipart
  submit and poll, latency percentiles, RSS sampling) and a fake-provider harness; the
  measurement runs and `docs/operator/capacity.md` were not finished.
- **Signed releases and SBOMs** (v1.0 04-01): keyless cosign signature of the image by
  digest, SPDX SBOMs (syft) for the image and each archive, GitHub build-provenance
  attestations, a "Verify a release" section for the install guide. `goreleaser check`
  passed; the workflow has not run.
- **Documentation site** (v1.0 04-02): MkDocs Material from `docs/` as-is (a hook rewrites
  links that leave `docs/` to GitHub URLs), Redocly-rendered API references, a Pages
  workflow on tags. `mkdocs build --strict` passes locally; Pages was never enabled.

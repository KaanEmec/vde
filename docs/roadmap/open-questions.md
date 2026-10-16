# Open questions and decisions

All questions below were answered by the project owner on 2026-10-16 (owner of both this
service and TravelInbox). Each entry records the decision and which parts of the roadmap
it changed. New questions go at the bottom under "Open".

## Decided

1. **Repository, module path and licence.** Apache-2.0. Binary and image named `vde`.
   Module path to be set when the repository is created. → v0.1 01-01.
2. **Version split.** Kept: v0.1 ships the engine and CLI, v0.2 the contract-conformant
   service.
3. **Providers in v0.1.** Anthropic, OpenAI-compatible and Google Gemini adapters.
   OpenRouter is a named, tested configuration of the OpenAI-compatible adapter (it
   speaks that API), not a separate adapter. Bedrock and Vertex stay in v0.3.
   → v0.1 02-04, 02-05; ADR 0004.
4. **TLS.** Both: the binary can terminate TLS, or trust a reverse proxy that sets
   `X-Forwarded-Proto`. → v0.2 01-01.
5. **Storage.** SQLite and a local blob directory in v0.2; Postgres and S3-compatible
   storage in v0.3. → ADR 0002.
6. **Schema optional.** Yes: schema-less mode at the service level, a superset of the
   contract. → v0.1 05-06.
7. **Email documents.** Core document type from v0.1: `message/rfc822` is parsed in the
   service, the body becomes the text representation, attachments are listed on the
   result for the caller to submit separately. → v0.1 04-04.
8. **Server-side PDF rendering.** Never. For PDFs the operator configures a model that
   reads PDFs natively, or the caller sends page images as the contract allows. A PDF
   that no configured endpoint can read is refused at submit with `unsupported_document`.
   → ADR 0006; v0.1 04-02; backlog.
9. **Escalation to a stronger model.** v0.3. → v0.3 epic 03.
10. **Administration.** Admin HTTP API ships in v0.2 alongside the CLI, with its own
    tokens and listener. → v0.2 epic 11.
11. **TravelInbox test assets.** Freely usable: the corpus, reference stub, prompts and
    schema are co-owned and are copied into `testdata/travelinbox/`. → v0.1 01-01, 04-03,
    05-03, 06-02; v0.3 06-01.
12. **Provider data handling.** Strictest available by default: adapters set
    no-training and zero or minimal retention options wherever the API exposes them, and
    document what must be set at account level. → v0.1 02-03, 02-04, 02-05; ADR 0004.
13. **HTML representation.** Raw HTML by default, sent to the model as text. A
    per-application `visible_text` option reduces it to readable text with link targets.
    The choice is part of `service_version`. → v0.1 04-02, 04-03; ADR 0006.
14. **Judge confidence.** The judge verdict carries an optional `confidence` (0 to 1),
    forwarded on the job as `judge_confidence` (null when absent). The owner also wants
    to be able to use a classifier model such as TypeSafe AI's Jev (typed decisions with calibrated probabilities instead of generated text) as the
    judge; a classifier yields a verdict and a confidence but not pointer-level feedback,
    so this is planned as a judge adapter in v0.3 with a defined fallback for feedback.
    → v0.1 05-01, 05-04, 05-05; v0.2 04-03; v0.3 02-02.
15. **`DELETE /v1/jobs/{id}`.** Implemented in v0.2. → v0.2 04-03.
16. **Default per-application limits.** 300 requests per minute, 30 submissions per
    minute, 20 jobs in progress, quota 1000 per day; all configurable. → v0.2 02-01,
    05-01, 05-02.

## Open

- ~~**Module path.**~~ Decided 2026-10-16: `github.com/kaanemec/vde`. See architecture
  overview, "Implementation decisions".
- ~~**Location of TravelInbox assets.**~~ Resolved 2026-10-16: the checkout at
  `~/Developer/TravelInbox` was available; assets are imported into `testdata/travelinbox/`
  by v0.1 01-01.

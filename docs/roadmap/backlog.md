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
| Cost accounting per application (tokens, money) | Metrics expose usage; billing is an operator concern. | An operator hosting the service for several tenants |
| Server-side PDF page rendering | Decided against on 2026-10-16 (ADR 0006): operators choose PDF-capable models or callers send pages. | A consumer that can do neither, with the owner reversing the decision |
| Helm chart | First deployment is docker compose on one VM. | A Kubernetes user |

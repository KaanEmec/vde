# Epic 01: Prompts and document preparation

Version: v1.2 · Status: planned · Depends on: v1.1 (released)

Nine experiments, cheapest first: four on the framings and the answer format (01, 05, 06,
07), three on how a document is laid out before a model reads it (02, 03, 04), a caller
guide (08) and, last and only if the earlier ones leave a gap, multi-pass extraction (09,
ADR first). Each job is one change, measured against the report before it and kept only
if it is not worse.

| # | Job | Kind | Status |
| --- | --- | --- | --- |
| 01 | [Prompt order and wording](01-prompt-order-and-wording.md) | prompt | planned |
| 02 | [Context header and page markers](02-context-header-and-page-markers.md) | preparation | in progress |
| 03 | [Email section labels](03-email-section-labels.md) | preparation | planned |
| 04 | [HTML data tables as Markdown](04-html-data-tables.md) | preparation | planned |
| 05 | [Extractor analysis block](05-extractor-analysis-block.md) | prompt | planned |
| 06 | [Judge evidence quotes](06-judge-evidence-quotes.md) | prompt | planned |
| 07 | [Native structured outputs](07-native-structured-outputs.md) | prompt | planned |
| 08 | [Schema guide for callers](08-caller-schema-guide.md) | docs | planned |
| 09 | [Multi-pass extraction (ADR 0012)](09-multi-pass-extraction.md) | architecture | planned |

Jobs 01 to 07 are sequential: each one's comparison report is the next one's baseline, so
one effect is measured at a time. 02, 03 and 04 touch disjoint files and could be built in
parallel, but are still measured one after another. 08 is documentation and can be written
at any time; it uses what 01 and 05 decide. 09 starts only after 07 and only with the
owner's go-ahead on its ADR.

## Measuring

Real-model runs cost money through OpenRouter or Nous, so **every real-model eval run in
this epic uses 3 to 5 documents, never more**:

- **Core set (4 cases, fixed for the whole epic):** `owner-01-flight` and
  `owner-03-lodging` (emails), `owner-02-flight` and `owner-10-event` (PDFs; the event
  invoice is the invented-values case). The same four in every run, so reports compare.
- **One run per job:** one `vde eval` invocation with `--judge always` and
  `--concurrency 1`. No reruns to average, no other judge policies, no full corpus, no
  TravelInbox corpus run (except job 08, and then 3 to 5 named cases).
- **A targeted case:** a job may swap one core case for another owner case its change
  targets (for example a second email for job 03), staying within 3 to 5 cases, and says
  so in its report row. The next job returns to the core set.
- **`--cases` always names the cases explicitly;** never a glob that could match more.
- `make eval`, `make scenarios` and `make conformance` use the fake provider and cost
  nothing; the cap does not apply to them.

Four documents give a signal, not a statistic: a difference of one field or one case is
noise unless the reason is visible (a mismatch that appears or disappears on a pointer the
change targets). When a result is unclear, the job stops and the owner decides; it does
not run more documents to find out.

Before job 01, record the baseline at the `v1.1.0` commit:

```
set -a; . testdata/user-docs/.env; set +a
go run ./cmd/vde eval --corpus testdata/user-docs/eval --config testdata/user-docs/vde.nous.yaml --judge always --concurrency 1 --cases owner-01-flight,owner-03-lodging,owner-02-flight,owner-10-event
```

Every job runs the same command with `--compare eval-reports/<previous>.json` and
records:

- cases fully correct, field mismatches per pointer, attempts per case, judge rejects,
  tokens and cost per case (a reasoning block costs output tokens);
- the decision: kept, or reverted with the numbers that decided it.

The reports go to `docs/quality/` with a row in [docs/quality.md](../../../quality.md).
Real-model runs use the owner's key and cost money: an agent runs them only when the
owner has said so for that job.

Rules for every job:

- A framing or representation change changes `service_version`
  (`internal/extract/version.go` hashes the framings; the representation policy strings
  are hashed already). Check that the version test changes.
- Grounding (`internal/ground`, ADR 0009) reads the document's own text. Text the service
  adds (labels, headers, markers) must never make an invented value grounded: grounding
  reads the text without the added lines, or the added lines carry no values.
- Answers, analysis blocks, quotes and documents never reach logs, errors, metrics, test
  output or reports.
- The fake-provider scenarios answer bare JSON; every parser change keeps accepting it.

## Research behind the jobs (2026-10-19)

Evidence for preparation techniques is mostly practitioner reports, so every item is an
experiment, not a known gain.

| Job | Finding | Sources |
| --- | --- | --- |
| 01 | Long documents above the instructions and the question last gave up to 30% better answers in Anthropic's tests; nullable fields beat optional ones because they separate "not in the document" from "forgotten" | [Anthropic long-context tips](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/long-context-tips), [freeCodeCamp](https://www.freecodecamp.org/news/how-to-get-reliable-structured-data-out-of-an-llm/) |
| 02 | Dates, years and time zones need the message's context; page markers let feedback point at a place | [Extend](https://www.extend.ai/resources/pdf-to-markdown-llm-pipelines) |
| 03 | Reply and forward detection exists in several libraries; booking details are often inside a forwarded message, so labelling beats stripping | [Talon](https://www.agentmail.to/docs/talon-reply-extraction), [email-reply-parser](https://github.com/web-ridge/email-reply-parser), [langmail](https://langmail.dev/) |
| 04 | Flattened tables lose which value belongs to which header; Markdown or HTML tables keep it | [DEV: tables and reading order](https://dev.to/hsinghsien-chen/pdf-to-markdown-for-rag-fix-reading-order-tables-and-headings-before-you-chunk-1knc), [ReleasePad](https://www.releasepad.io/blog/html-vs-markdown-the-optimal-format-for-llm-content-ingestion/) |
| 05, 06 | The format instruction, more than decoding constraints, costs accuracy; reasoning freely first and answering in a fixed format afterwards recovers most of it; quoting first helps on long documents | ["The Format Tax"](https://arxiv.org/pdf/2604.03616), ["Thinking Before Constraining"](https://arxiv.org/html/2601.07525v1), [Anthropic long-context tips](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/long-context-tips) |
| 06 | Whether a field has a located source separates right from wrong values well | [Per-field selective risk control](https://arxiv.org/pdf/2608.14639) |
| 07 | Constrained decoding guarantees the shape, not the content; keep reasoning outside the constraint | [Together AI](https://docs.together.ai/learn/structured-outputs), [CRANE](https://icml.cc/virtual/2025/poster/43624) |
| 08 | Field descriptions are read by the model; declare type, format, allowed values, units and the meaning of null | [LlamaExtract schema design](https://developers.llamaindex.ai/llamaparse/extract/guides/configuring-extract/#schema-design-and-restrictions) |
| 09 | Frontier models are still unreliable on large schemas and long outputs | [ExtractBench](https://arxiv.org/html/2602.12247) |

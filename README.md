# vde: verified document extraction

`vde` is a self-hosted Go service that turns one document (PDF, image, HTML, text, email)
into one JSON object. An extractor model produces a candidate, a separately configured
judge model checks it against the source, and the loop repeats with the judge's feedback
until the judge accepts or a bounded attempt limit is reached. Callers supply the
instructions and, optionally, a JSON Schema per request; the service holds no domain
knowledge. Providers: Anthropic, OpenAI-compatible endpoints (OpenAI, OpenRouter, Ollama,
vLLM), Google Gemini, AWS Bedrock and Vertex AI (not yet verified live) and a classifier-style
judge, with ordered failover per role.

- **Judge policy per application**: `always` (every result judged, the default), `never`
  (results end `extracted`, no judge) or `auto` (results whose values the document prints
  skip the judge, with a sampled check); see [judge policy](docs/operator/judge-policy.md).
- **Grounding check**: a deterministic check that every extracted value occurs in the
  document text, which catches invented dates, names and amounts that judges accepted.
- **PDFs without caller pages**: the service reads a PDF's text layer or renders its pages
  itself (pdfium as WebAssembly, still one static cgo-free binary), so models without PDF
  input work; see [PDF preprocessing](docs/operator/pdf.md).

## Status

v0.1 to v0.4 are implemented: engine, CLI, HTTP API, jobs, retention, operations, admin API,
container image and compose example, plus Postgres and S3 storage, Bedrock and Vertex
adapters, a classifier judge adapter, escalation, image conversion, synchronous and webhook
delivery, an evaluation harness and multi-replica operation, and (v0.4) judge policies, the
grounding check and PDF preparation in the service. v0.4 is closed with the owner's
comparison of the judge policies on real documents still to run. Verified against the fake
provider, the TravelInbox contract's live test and one real model; the Bedrock and Vertex
adapters have not run live, and the first evaluation shows a high false-accept rate that needs
instruction work (see [quality](docs/quality.md)). Do not treat it as production-ready. v1.0
(API freeze, security review) is planned. The [roadmap](docs/roadmap/README.md) says what is
done and what is next.

## Quick start

Requires Go 1.26 or newer; the binary is pure Go (`CGO_ENABLED=0`), about 36 MB on
linux/amd64 (pdfium embedded as WebAssembly). Budget about 500 MB of memory per
`documents.pdf.workers` (default 1) when the service prepares PDFs; a deployment whose
models all read PDFs never starts pdfium.

```
make build                                       # bin/vde (or: docker build -t vde .)
export ANTHROPIC_API_KEY=...                     # any value is enough for the check below
bin/vde config check --config examples/vde.dev.yaml
bin/vde extract --config examples/vde.dev.yaml --document examples/samples/booking.txt \
  --extractor-instructions examples/samples/extractor.md --judge-instructions examples/samples/judge.md \
  --provider-script testdata/scenarios/accept-first-try.yaml     # offline, scripted fake provider
# To serve: copy the file to vde.yaml and add an `applications:` entry, then
bin/vde serve --config vde.yaml                  # real provider key required
bin/vde token create --config vde.yaml --app <app id> --scope jobs --expires 30d   # prints the token once
```

Declaring an application is described in
[applications and tokens](docs/operator/applications-and-tokens.md); submit a document with
`curl` as shown in the [API guide](docs/api/guide.md). To measure quality, run
`bin/vde eval --corpus testdata/eval --config vde.yaml` ([evaluation](docs/operator/evaluation.md)).
With Postgres and S3, run `bin/vde migrate --config vde.yaml` once, then start processes with
`bin/vde serve --role api|worker|all` ([storage](docs/operator/storage.md)). For Docker, follow the
[compose example](examples/compose/README.md). Other commands: `make check` (vet, lint, race
tests), `make cross`, `bin/vde version`.

## Documentation

- [Operator guide](docs/operator/README.md): install, configuration, tokens, retention, operations, troubleshooting
- [API guide](docs/api/guide.md) and the [admin API](docs/api/admin.md)
- [Security and privacy](docs/security.md): threat model, controls, limits, operator checklist
- [Provider adapters](docs/providers/README.md): comparison, what each sends, retention notes
- [TravelInbox conformance review](docs/conformance/travelinbox-v1-review.md) and the
  [contract](FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md), the acceptance test of the first deployable release
- [Roadmap](docs/roadmap/README.md)
- [Project brief](verified-document-extraction.md), [architecture overview](docs/architecture/overview.md) and [decisions](docs/architecture/decisions/)
- [Examples](examples/README.md): development configurations and sample documents
- [Changelog](CHANGELOG.md), [contributing](CONTRIBUTING.md), [security policy](SECURITY.md)

## License

Apache License 2.0, see [LICENSE](LICENSE).

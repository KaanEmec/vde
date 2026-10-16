# vde: verified document extraction

`vde` is a self-hosted Go service that turns one document (PDF, image, HTML, text, email)
into one JSON object. An extractor model produces a candidate, a separately configured
judge model checks it against the source, and the loop repeats with the judge's feedback
until the judge accepts or a bounded attempt limit is reached. Callers supply the
instructions and, optionally, a JSON Schema per request; the service holds no domain
knowledge. Providers: Anthropic, OpenAI-compatible endpoints (OpenAI, OpenRouter, Ollama,
vLLM) and Google Gemini, with ordered failover per role.

## Status

v0.2 (the service) is implemented: engine, CLI, HTTP API, jobs, retention, operations, admin
API, container image and compose example. It is pending live verification: it has not yet been
run against real providers or the TravelInbox contract's live evaluation, so do not treat it as
production-ready. The [roadmap](docs/roadmap/README.md) says what is done and what is next.

## Quick start

Requires Go 1.26 or newer; the binary is pure Go (`CGO_ENABLED=0`).

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
`curl` as shown in the [API guide](docs/api/guide.md). For Docker, follow the
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

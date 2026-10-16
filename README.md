# vde: verified document extraction

`vde` is a self-hosted Go service that turns one document (PDF, image, HTML, text, email)
into one JSON object. An extractor model produces a candidate, a separately configured
judge model checks it against the source, and the loop repeats with the judge's feedback
until the judge accepts or a bounded attempt limit is reached. Callers supply the
instructions and, optionally, a JSON Schema per request; the service holds no domain
knowledge. Providers: Anthropic, OpenAI-compatible endpoints (OpenAI, OpenRouter, Ollama,
vLLM) and Google Gemini, with ordered failover per role.

## Status

Early implementation of v0.1 (core engine). Nothing here is ready for production use yet;
the [roadmap](docs/roadmap/README.md) says what is done.

## Build

Requires Go 1.25 or newer (developed with 1.27). The binary is pure Go (`CGO_ENABLED=0`).

```
make build      # bin/vde
make check      # vet, golangci-lint, go test -race, build
make cross      # static linux/amd64 and linux/arm64 binaries
bin/vde version
bin/vde config check --config vde.yaml
```

## Documentation

- [Project brief](verified-document-extraction.md)
- [Architecture overview](docs/architecture/overview.md) and [decisions](docs/architecture/decisions/)
- [Roadmap](docs/roadmap/README.md)
- [Configuration reference](docs/operator/configuration.md)
- [TravelInbox contract](FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md), the acceptance test of the first deployable release

## License

Apache License 2.0, see [LICENSE](LICENSE).

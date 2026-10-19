# ADR 0001: Go with the standard library HTTP stack

Status: accepted (planning), 2026-10-16

## Context

The service is a small HTTP API with a handful of routes, multipart uploads, bearer
authentication and a few middleware concerns. The project brief asks for an easily
installed, single-binary service; the user asked for a lean, not over-abstracted codebase.

## Decision

- Go 1.23 or newer. `net/http` with the pattern-matching `ServeMux` for routing,
  `log/slog` for logging, `database/sql` for storage, `encoding/json` for JSON.
- Middleware is a plain `func(http.Handler) http.Handler` chain written in the project;
  no router or web framework.
- Dependencies allowed because they replace real work: a JSON Schema 2020-12 validator
  (`github.com/santhosh-tekuri/jsonschema/v6`), a cgo-free SQLite driver
  (`modernc.org/sqlite`), a YAML parser (`gopkg.in/yaml.v3` or `github.com/goccy/go-yaml`),
  a ULID library, and the Prometheus client. HTML-to-text uses `golang.org/x/net/html`.
- No dependency-injection container, no code generation other than `go generate` for
  embedding fixtures, no ORM. SQL is written by hand against a small `JobStore` interface.

## Consequences

- Fewer moving parts and a build that cross-compiles to `linux/amd64` and `linux/arm64`
  without cgo, which the TravelInbox contract requires.
- Some boilerplate (middleware chain, SQL) is written by hand; it stays small because the
  API has five routes.
- OpenAPI is maintained as a document (`api/openapi.yaml`), not generated from code; a test
  compares routes and error codes against it.

## Amendment 2026-10-19

The YAML parser is `go.yaml.in/yaml/v3`, the maintained continuation of the archived
`gopkg.in/yaml.v3` with the same API, and ULIDs come from the standard library
(`job.NewULID`) instead of a ULID library (v1.1 job 07-01).

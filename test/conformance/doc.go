// Package conformance is the black-box conformance suite for the TravelInbox service
// contract, version 1 (FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md). It holds tests
// only; this file exists for the package documentation.
//
// Local mode (the default, `make conformance`): TestMain builds the vde binary into a
// temporary directory, starts an in-process fake OpenAI-compatible chat completions
// server whose answers each test scripts, writes a configuration with a primary and a
// fallback endpoint per role pointing at it, runs `vde serve` on a free loopback port
// with a temporary data directory, and issues tokens with `vde token create`. The tests
// then talk to the service over real HTTP only, so the real openai_compatible adapter,
// router, worker and store are exercised. Tests that need another configuration (an
// unreachable judge group, an unreachable primary extractor, a short job timeout, a
// trusted proxy) start their own `vde serve` the same way.
//
// Deployment mode: with VDE_CONFORMANCE_URL (the service's base URL, without /v1) and
// VDE_CONFORMANCE_TOKEN (a token with the jobs scope) set, no binary is built and no
// server is started. The tests that need only HTTP run against that deployment and the
// ones that need the fake provider, the CLI or the data directory are skipped.
// VDE_CONFORMANCE_FOREIGN_TOKEN, a token of another application, enables the
// foreign-token checks. Jobs are real extractions there and cost provider calls:
//
//	VDE_CONFORMANCE_URL=https://extract.example.com VDE_CONFORMANCE_TOKEN=... \
//	  go test ./test/conformance/ -count=1 -v
//
// The MUST traceability table is docs/conformance/travelinbox-v1.md.
package conformance

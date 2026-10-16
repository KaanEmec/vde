// Package api ships the service's OpenAPI document, api/openapi.yaml, a superset of
// TravelInbox's FromTravelApp/extraction-service-api.yaml (ADR 0005). The HTTP layer
// (internal/api) serves it at GET /v1/openapi.yaml.
package api

import _ "embed"

// OpenAPI is the content of openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte

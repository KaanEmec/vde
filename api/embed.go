// Package api ships the service's OpenAPI documents: api/openapi.yaml, a superset of
// TravelInbox's FromTravelApp/extraction-service-api.yaml (ADR 0005) that the HTTP layer
// (internal/api) serves at GET /v1/openapi.yaml, and api/admin-openapi.yaml, the admin
// API (epic 11).
package api

import _ "embed"

// OpenAPI is the content of openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte

// AdminOpenAPI is the content of admin-openapi.yaml.
//
//go:embed admin-openapi.yaml
var AdminOpenAPI []byte

package config

import (
	"bytes"
	_ "embed"
)

// schemaJSON is the JSON Schema (draft 2020-12) of the configuration file, written by
// hand from the structs; schema_test.go keeps the two in step. It is the frozen 1.x key
// set for editors (vde config schema prints it); its descriptions are one-line hints, and
// docs/operator/configuration.md describes each key. Nothing validates against it at run
// time: config check, serve and extract run Parse and Validate alone.
//
//go:embed schema.json
var schemaJSON []byte

// Schema returns the JSON Schema of the configuration file.
func Schema() []byte { return bytes.Clone(schemaJSON) }

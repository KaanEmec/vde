package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

// schemaJSON is the JSON Schema (draft 2020-12) of the configuration file, written by
// hand from the structs; schema_test.go keeps the two in step. It is the frozen 1.x key
// set: editors validate against it (vde config schema prints it), and config check
// reports its violations after Validate's. Validate stays authoritative: serve and
// extract do not read the schema.
//
//go:embed schema.json
var schemaJSON []byte

// schemaURL is the schema's $id, under which it is compiled; nothing is fetched from it.
const schemaURL = "https://github.com/kaanemec/vde/internal/config/schema.json"

// Schema returns the JSON Schema of the configuration file.
func Schema() []byte { return bytes.Clone(schemaJSON) }

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("config: schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("config: schema: %w", err)
	}
	s, err := c.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("config: schema: %w", err)
	}
	return s, nil
})

var schemaPrinter = message.NewPrinter(language.English)

// CheckSchema validates the YAML file data against the schema and returns one error Issue
// per violation, keyed by the offending key's path in the style of Validate
// (applications[0].judge.policy; "(root)" for the file itself), sorted by key. A null
// value counts as absent, as in Parse. The error is for a file that is not YAML.
func CheckSchema(data []byte) (Issues, error) {
	s, err := compiledSchema()
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	// Through JSON, so that the instance holds only JSON types (timestamps become
	// strings, numbers json.Number).
	js, err := json.Marshal(normalizeYAML(raw))
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	err = s.Validate(inst)
	if err == nil {
		return nil, nil
	}
	ve, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return nil, fmt.Errorf("config: schema: %w", err)
	}
	var issues Issues
	seen := map[Issue]bool{}
	add := func(loc []string, msg string) {
		i := Issue{Key: schemaPath(inst, loc), Message: msg}
		if !seen[i] {
			seen[i] = true
			issues = append(issues, i)
		}
	}
	collectViolations(ve, add)
	slices.SortFunc(issues, cmpIssue)
	return issues, nil
}

func cmpIssue(a, b Issue) int {
	if c := strings.Compare(a.Key, b.Key); c != 0 {
		return c
	}
	return strings.Compare(a.Message, b.Message)
}

// collectViolations reports the leaves of a validation error tree. An unknown key and a
// missing required key are reported at the key itself.
func collectViolations(e *jsonschema.ValidationError, add func(loc []string, msg string)) {
	loc := e.InstanceLocation
	switch k := e.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		for _, p := range k.Properties {
			add(append(slices.Clip(loc), p), "unknown key")
		}
	case *kind.Required:
		for _, p := range k.Missing {
			add(append(slices.Clip(loc), p), "is required")
		}
	case *kind.Pattern:
		// The regular expression means little to an operator; the description says
		// what is expected.
		msg := fmt.Sprintf("%q does not have the expected form", k.Got)
		if d := schemaDescription(e.SchemaURL); d != "" {
			msg += "; expected: " + strings.TrimSuffix(d, ".")
		}
		add(loc, msg)
	default:
		if len(e.Causes) == 0 {
			add(loc, k.LocalizedString(schemaPrinter))
		}
	}
	for _, c := range e.Causes {
		collectViolations(c, add)
	}
}

var schemaDoc = sync.OnceValue(func() map[string]any {
	var doc map[string]any
	_ = json.Unmarshal(schemaJSON, &doc) // compiledSchema reports a broken schema
	return doc
})

// schemaDescription returns the description of the schema object at an absolute schema
// location (schemaURL#/$defs/duration), or "".
func schemaDescription(location string) string {
	base, frag, _ := strings.Cut(location, "#")
	if base != schemaURL {
		return ""
	}
	var node any = schemaDoc()
	for tok := range strings.SplitSeq(strings.TrimPrefix(frag, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		m, ok := node.(map[string]any)
		if !ok {
			return ""
		}
		node = m[tok]
	}
	m, _ := node.(map[string]any)
	d, _ := m["description"].(string)
	return d
}

// normalizeYAML turns a decoded YAML value into JSON-compatible values: mapping keys
// become strings, and mapping entries whose value is null are dropped (Parse leaves
// such keys at their default, so the schema must not see them).
func normalizeYAML(v any) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			if child != nil {
				out[k] = normalizeYAML(child)
			}
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			if child != nil {
				out[fmt.Sprint(k)] = normalizeYAML(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			out[i] = normalizeYAML(child)
		}
		return out
	}
	return v
}

// schemaPath renders an instance location as a key path: endpoints.local.timeout,
// applications[0].id. Indexes are told from numeric map keys by the instance.
func schemaPath(inst any, loc []string) string {
	if len(loc) == 0 {
		return "(root)"
	}
	var b strings.Builder
	node := inst
	for _, tok := range loc {
		switch n := node.(type) {
		case []any:
			b.WriteString("[" + tok + "]")
			if i, err := strconv.Atoi(tok); err == nil && i >= 0 && i < len(n) {
				node = n[i]
			} else {
				node = nil
			}
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(tok)
			if m, ok := n.(map[string]any); ok {
				node = m[tok]
			} else {
				node = nil
			}
		}
	}
	return b.String()
}

package extract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/ground"
)

const travelSchemaPath = "../../testdata/travelinbox/extraction-schema.json"

func travelSchema(t *testing.T) *Schema {
	t.Helper()
	data, err := os.ReadFile(travelSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := CompileSchema(data)
	if err != nil {
		t.Fatalf("CompileSchema: %v", err)
	}
	return s
}

// truth returns a corpus ground-truth result decoded for editing.
func truth(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../testdata/travelinbox/corpus", name, "truth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCompileSchema_invalid(t *testing.T) {
	tests := []struct {
		name, schema string
	}{
		{"not JSON", `{"type": `},
		{"trailing data", `{} {}`},
		{"wrong keyword type", `{"type": 5}`},
		{"bad pattern", `{"pattern": "("}`},
		{"remote ref", `{"$ref": "https://example.com/other.json"}`},
		{"relative ref", `{"$ref": "other.json"}`},
		{"file ref", `{"$ref": "file:///etc/passwd"}`},
		{"remote ref with id", `{"$id": "https://example.com/s", "properties": {"a": {"$ref": "defs.json#/a"}}}`},
		{"unknown draft", `{"$schema": "https://example.com/meta"}`},
		{"dangling local ref", `{"$ref": "#/$defs/missing"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CompileSchema([]byte(tt.schema)); !errors.Is(err, ErrInvalidSchema) {
				t.Fatalf("CompileSchema(%s) = %v, want ErrInvalidSchema", tt.schema, err)
			}
		})
	}
}

func TestCompileSchema_topLevelArrays(t *testing.T) {
	tests := []struct {
		schema string
		want   bool
	}{
		{`{"type": "object"}`, false},
		{`{"type": ["object", "null"]}`, false},
		{`{"type": "array"}`, true},
		{`{"type": ["array", "object"]}`, true},
		{`{}`, true},
		{`true`, true},
	}
	for _, tt := range tests {
		t.Run(tt.schema, func(t *testing.T) {
			s, err := CompileSchema([]byte(tt.schema))
			if err != nil {
				t.Fatal(err)
			}
			if s.allowArray != tt.want {
				t.Errorf("allowArray = %v, want %v", s.allowArray, tt.want)
			}
		})
	}
}

// TestSchema_Validate_corpus: every ground-truth result of the TravelInbox corpus
// validates (decision 11).
func TestSchema_Validate_corpus(t *testing.T) {
	s := travelSchema(t)
	dirs, err := filepath.Glob("../../testdata/travelinbox/corpus/*/truth.json")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	for _, path := range dirs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if fb := s.Validate(data); fb != nil {
			t.Errorf("%s: %+v", path, fb)
		}
	}
}

func TestSchema_Validate_travel(t *testing.T) {
	s := travelSchema(t)
	type want struct{ pointer, keyword, contains string }
	tests := []struct {
		name string
		base string
		edit func(doc map[string]any, r map[string]any)
		want []want // exactly these entries, in order
	}{
		{
			name: "missing confidence for a present field",
			base: "001-flight",
			edit: func(_, r map[string]any) { delete(r["confidence"].(map[string]any), "provider") },
			want: []want{{"/reservations/0/confidence", "required", "'provider'"}},
		},
		{
			name: "wrong pattern",
			base: "001-flight",
			edit: func(_, r map[string]any) { r["starts_at_local"] = "2027-06-14 10:35" },
			want: []want{{"/reservations/0/starts_at_local", "pattern", "expected: RFC 3339 date-time without offset"}},
		},
		{
			name: "unknown property",
			base: "016-hotel",
			edit: func(_, r map[string]any) { r["loyalty_number"] = "X" },
			want: []want{{"/reservations/0/loyalty_number", "additionalProperties", "expected only booking_ref,"}},
		},
		{
			name: "wrong kind details shape",
			base: "001-flight",
			edit: func(_, r map[string]any) { r["details"].(map[string]any)["room"] = "12" },
			want: []want{{"/reservations/0/details/room", "additionalProperties", "expected only cabin, carrier"}},
		},
		{
			name: "details of another kind",
			base: "016-hotel",
			edit: func(_, r map[string]any) { r["details"] = map[string]any{"gate": "B2"} },
			want: []want{{"/reservations/0/details/gate", "additionalProperties", "checkin_time"}},
		},
		{
			name: "unknown kind",
			base: "001-flight",
			edit: func(_, r map[string]any) { r["kind"] = "boat" },
			want: []want{
				{"/reservations/0", "oneOf", "matches none of the alternatives"},
				{"/reservations/0/kind", "enum", "'flight'"},
			},
		},
		{
			name: "unknown confidence name",
			base: "001-flight",
			edit: func(_, r map[string]any) { r["confidence"].(map[string]any)["seat"] = 1.0 },
			want: []want{{"/reservations/0/confidence/seat", "propertyNames", "expected one of kind, title"}},
		},
		{
			name: "missing required properties",
			base: "001-flight",
			edit: func(doc, _ map[string]any) {
				delete(doc, "schema_version")
				delete(doc, "reservations")
			},
			want: []want{{"", "required", "'schema_version'"}, {"", "required", "'reservations'"}},
		},
		{
			name: "wrong type and const",
			base: "001-flight",
			edit: func(doc, r map[string]any) {
				doc["schema_version"] = 2
				r["travellers"] = "two"
			},
			want: []want{
				{"/reservations/0/travellers", "type", "want null or array"},
				{"/schema_version", "const", "value must be 1"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := truth(t, tt.base)
			tt.edit(doc, doc["reservations"].([]any)[0].(map[string]any))
			got := s.Validate(mustJSON(t, doc))
			if len(got) != len(tt.want) {
				t.Fatalf("Validate = %d entries %+v, want %d", len(got), got, len(tt.want))
			}
			for i, w := range tt.want {
				g := got[i]
				if g.Pointer != w.pointer || g.Source != SourceSchema ||
					!strings.Contains(g.Message, "(keyword "+w.keyword+")") || !strings.Contains(g.Message, w.contains) {
					t.Errorf("entry %d = %+v, want pointer %q, keyword %s, containing %q", i, g, w.pointer, w.keyword, w.contains)
				}
			}
		})
	}
}

func TestSchema_Validate(t *testing.T) {
	tests := []struct {
		name, schema, candidate string
		want                    []string // "pointer|message substring"
	}{
		{"valid", `{"type": "object"}`, `{}`, nil},
		{"format", `{"properties": {"at": {"format": "date-time"}}}`, `{"at": "yesterday"}`, []string{"/at|(keyword format)"}},
		{"format valid", `{"properties": {"at": {"format": "date-time"}}}`, `{"at": "2026-10-16T09:12:03Z"}`, nil},
		{"escaped pointer", `{"properties": {"a/b": {"type": "string"}, "c~d": {"type": "string"}}}`, `{"a/b": 1, "c~d": 2}`, []string{"/a~1b|`a/b`: got number, want string", "/c~0d|(keyword type)"}},
		{"oneOf several match", `{"oneOf": [{"type": "object"}, {"required": []}]}`, `{}`, []string{"|matches more than one"}},
		{"oneOf no discriminator", `{"oneOf": [{"type": "string"}, {"type": "number"}]}`, `[]`, []string{"|the result: matches none of the alternatives (keyword oneOf)"}},
		{"anyOf", `{"anyOf": [{"type": "string"}, {"type": "number"}]}`, `{}`, []string{"|(keyword anyOf)"}},
		{"anyOf discriminated", `{"anyOf": [{"properties": {"k": {"const": "a"}, "v": {"type": "string"}}}, {"properties": {"k": {"const": "b"}, "v": {"type": "number"}}}]}`, `{"k": "a", "v": 1}`, []string{"/v|got number, want string"}},
		{"array item", `{"type": "array", "items": {"type": "integer"}}`, `[1, "x"]`, []string{"/1|`1`: got string, want integer"}},
		{"not", `{"not": {"type": "object"}}`, `{}`, []string{"|(keyword schema)"}},
		{"false schema", `{"properties": {"a": false}}`, `{"a": 1}`, []string{"/a|(keyword schema)"}},
		{"not JSON", `{}`, `{`, []string{"|not valid JSON"}},
		{"description from nested id ignored", `{"$defs": {"x": {"$id": "urn:x", "description": "D", "type": "string"}}, "properties": {"a": {"$ref": "urn:x"}}}`, `{"a": 1}`, []string{"/a|(keyword type)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := CompileSchema([]byte(tt.schema))
			if err != nil {
				t.Fatal(err)
			}
			got := s.Validate(json.RawMessage(tt.candidate))
			if len(got) != len(tt.want) {
				t.Fatalf("Validate = %+v, want %d entries", got, len(tt.want))
			}
			for i, w := range tt.want {
				ptr, sub, _ := strings.Cut(w, "|")
				if got[i].Pointer != ptr || !strings.Contains(got[i].Message, sub) {
					t.Errorf("entry %d = %+v, want pointer %q containing %q", i, got[i], ptr, sub)
				}
			}
		})
	}
}

func TestSchema_Validate_cap(t *testing.T) {
	s, err := CompileSchema([]byte(`{"type": "array", "items": {"type": "integer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	items := make([]string, 60)
	for i := range items {
		items[i] = fmt.Sprintf("%q", "x")
	}
	got := s.Validate(json.RawMessage("[" + strings.Join(items, ",") + "]"))
	if len(got) != maxSchemaFeedback+1 {
		t.Fatalf("Validate = %d entries, want %d", len(got), maxSchemaFeedback+1)
	}
	if last := got[maxSchemaFeedback]; last.Pointer != "" || !strings.Contains(last.Message, "10 more") {
		t.Errorf("summary = %+v, want 10 more on the whole result", last)
	}
}

// TestCompileSchema_grounding: CompileSchema reads x-vde-grounding (ADR 0009); an unknown
// value is an invalid schema, and a nil schema has nil rules (every leaf critical).
func TestCompileSchema_grounding(t *testing.T) {
	s, err := CompileSchema([]byte(`{"type": "object", "properties": {
		"city": {"type": "string", "x-vde-grounding": "derived"},
		"date": {"type": "string", "x-vde-grounding": "critical"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if rule, _ := s.GroundingRules()([]string{"city"}); rule != ground.RuleDerived {
		t.Errorf("city rule = %v, want derived", rule)
	}
	if rule, _ := s.GroundingRules()([]string{"date"}); rule != ground.RuleCritical {
		t.Errorf("date rule = %v, want critical", rule)
	}
	_, err = CompileSchema([]byte(`{"type": "object", "properties": {"a": {"x-vde-grounding": "maybe"}}}`))
	if !errors.Is(err, ErrInvalidSchema) || !errors.Is(err, ground.ErrUnknownRule) {
		t.Errorf("unknown annotation: err = %v, want ErrInvalidSchema wrapping ground.ErrUnknownRule", err)
	}
	var none *Schema
	if none.GroundingRules() != nil {
		t.Error("a nil schema has rules")
	}
}

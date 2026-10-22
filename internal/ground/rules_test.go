package ground

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseRule(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]Rule{"check": RuleCheck, "derived": RuleDerived, "skip": RuleSkip, "critical": RuleCritical, "convention": RuleConvention} {
		if got, err := ParseRule(s); err != nil || got != want {
			t.Errorf("ParseRule(%q) = %v, %v, want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"", "maybe", "Critical", "conventional"} {
		if _, err := ParseRule(s); !errors.Is(err, ErrUnknownRule) {
			t.Errorf("ParseRule(%q) error = %v, want ErrUnknownRule", s, err)
		}
	}
}

const rulesSchema = `{
  "type": "object",
  "$defs": {
    "place": {"type": "object", "properties": {"city": {"type": "string", "x-vde-grounding": "derived"}, "name": {"type": "string"}}},
    "node": {"type": "object", "properties": {"label": {"type": "string"}, "next": {"$ref": "#/$defs/node"}}},
    "a~b/c": {"type": "string", "x-vde-grounding": "skip"}
  },
  "properties": {
    "venue": {"$ref": "#/$defs/place"},
    "override": {"$ref": "#/$defs/place", "x-vde-grounding": "critical"},
    "meta": {
      "type": "object", "x-vde-grounding": "skip",
      "properties": {"source": {"type": "string"}, "page": {"type": "string", "x-vde-grounding": "check"}}
    },
    "travellers": {"type": "array", "x-vde-grounding": "critical", "items": {"type": "object", "properties": {"name": {"type": "string"}}}},
    "legs": {"type": "array", "prefixItems": [{"x-vde-grounding": "derived"}], "items": {"type": "string"}},
    "tuple": {"type": "array", "items": [{"x-vde-grounding": "skip"}, {"type": "string"}]},
    "class": {"enum": ["economy", "business", 1, null]},
    "kind": {"const": "invoice"},
    "branchy": {"anyOf": [{"type": "null"}, {"type": "object", "properties": {"code": {"x-vde-grounding": "derived"}}}]},
    "combined": {"allOf": [{"properties": {"x": {"type": "string"}}}, {"x-vde-grounding": "skip"}]},
    "tags": {"type": "object", "additionalProperties": {"type": "string", "x-vde-grounding": "derived"}},
    "chain": {"$ref": "#/$defs/node"},
    "escaped": {"$ref": "#/$defs/a~0b~1c"},
    "self": {"$ref": "#"},
    "remote": {"$ref": "https://example.com/schema.json"},
    "carrier": {
      "type": "object", "x-vde-grounding": "critical",
      "properties": {"name": {"type": "string", "x-vde-grounding": "convention"}, "flight": {"type": "string"}}
    },
    "details": {
      "type": "object", "x-vde-grounding": "convention",
      "properties": {"coach": {"type": "string"}, "pnr": {"type": "string", "x-vde-grounding": "critical"}}
    },
    "notes": {"type": "string"}
  }
}`

func TestRulesFromSchema(t *testing.T) {
	t.Parallel()
	rules, annotated, err := RulesFromSchema([]byte(rulesSchema))
	if err != nil || !annotated {
		t.Fatalf("RulesFromSchema() annotated %v, error %v; want true, nil", annotated, err)
	}
	tests := []struct {
		loc  string
		rule Rule
		enum []string
	}{
		{"venue/city", RuleDerived, nil},
		{"venue/name", RuleNone, nil},
		{"override/name", RuleCritical, nil},
		{"override/city", RuleDerived, nil},
		{"meta/source", RuleSkip, nil},
		{"meta/page", RuleCheck, nil},
		{"travellers/0/name", RuleCritical, nil},
		{"travellers/7/name", RuleCritical, nil},
		{"legs/0", RuleDerived, nil},
		{"legs/1", RuleNone, nil},
		{"tuple/0", RuleSkip, nil},
		{"tuple/1", RuleNone, nil},
		{"tuple/2", RuleNone, nil},
		{"class", RuleNone, []string{"economy", "business", "1", "null"}},
		{"kind", RuleNone, []string{"invoice"}},
		{"branchy/code", RuleDerived, nil},
		{"combined/x", RuleSkip, nil},
		{"tags/anything", RuleDerived, nil},
		{"chain/next/next/next/label", RuleNone, nil},
		{"escaped", RuleSkip, nil},
		{"self/venue/city", RuleDerived, nil},
		{"remote/x", RuleNone, nil},
		{"unknown/deep/path", RuleNone, nil},
		{"carrier/name", RuleConvention, nil}, // convention inside a critical object
		{"carrier/flight", RuleCritical, nil},
		{"details/coach", RuleConvention, nil}, // inherited
		{"details/pnr", RuleCritical, nil},     // overridden by a deeper annotation
		{"notes", RuleNone, nil},               // the schema is annotated: no default skip
	}
	for _, tt := range tests {
		t.Run(tt.loc, func(t *testing.T) {
			t.Parallel()
			rule, enum := rules(strings.Split(tt.loc, "/"))
			if rule != tt.rule || !slices.Equal(enum, tt.enum) {
				t.Errorf("rules(%s) = %v, %q, want %v, %q", tt.loc, rule, enum, tt.rule, tt.enum)
			}
		})
	}
}

func TestRulesFromSchema_defaultSkip(t *testing.T) {
	t.Parallel()
	rules, annotated, err := RulesFromSchema([]byte(`{"type":"object","properties":{
		"notes":{"type":"array","items":{"type":"string"}},
		"items":{"type":"array","items":{"type":"object","properties":{"confidence":{"type":"number"},"name":{"type":"string"}}}}}}`))
	if err != nil || annotated {
		t.Fatalf("RulesFromSchema() annotated %v, error %v; want false, nil", annotated, err)
	}
	tests := []struct {
		loc  string
		want Rule
	}{
		{"notes/0", RuleSkip},
		{"items/3/confidence", RuleSkip},
		{"items/3/name", RuleNone},
		{"confidence", RuleSkip}, // by name, even outside the schema's properties
		{"0", RuleNone},
	}
	for _, tt := range tests {
		if got, _ := rules(strings.Split(tt.loc, "/")); got != tt.want {
			t.Errorf("rules(%s) = %v, want %v", tt.loc, got, tt.want)
		}
	}
}

func TestRulesFromSchema_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, schema string
		unknown      bool
	}{
		{"unknown value", `{"properties":{"a":{"x-vde-grounding":"maybe"}}}`, true},
		{"not a string", `{"properties":{"a":{"items":[{"x-vde-grounding":1}]}}}`, true},
		{"invalid json", `{"properties":`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rules, _, err := RulesFromSchema([]byte(tt.schema))
			if err == nil || rules != nil {
				t.Fatalf("RulesFromSchema() = %v, %v, want an error", rules != nil, err)
			}
			if errors.Is(err, ErrUnknownRule) != tt.unknown {
				t.Errorf("error %v: errors.Is(ErrUnknownRule) = %v, want %v", err, !tt.unknown, tt.unknown)
			}
		})
	}
}

func TestRulesFromSchema_recursive(t *testing.T) {
	t.Parallel()
	rules, _, err := RulesFromSchema([]byte(`{"$ref":"#","allOf":[{"$ref":"#"}],"x-vde-grounding":"skip"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := rules([]string{"a", "b"}); got != RuleSkip {
		t.Errorf("rules = %v, want skip", got)
	}
}

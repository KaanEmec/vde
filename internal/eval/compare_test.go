package eval_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/eval"
)

func mustRules(t *testing.T, yaml string) eval.Rules {
	t.Helper()
	r, err := eval.ParseRules([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	return r
}

func TestRules_Compare(t *testing.T) {
	t.Parallel()
	const rules = `
default: exact
rules:
  - {pointer: /notes, mode: ignore}
  - {pointer: /items/*/name, mode: normalized}
  - {pointer: /items/*/price, mode: numeric, tolerance: 0.01}
  - {pointer: /items/0/price, mode: exact}
  - {pointer: /tags, mode: set}
  - {pointer: /people, mode: set, elements: normalized}
`
	const expected = `{
		"title": "Receipt",
		"notes": "free text",
		"missing": null,
		"items": [{"name": "Café  au lait.", "price": 3.5}, {"name": "Bun", "price": "1.20"}],
		"tags": ["a", "b", "b"],
		"people": [{"name": "Ada Lovelace"}, {"name": "Alan Turing"}]
	}`
	tests := []struct {
		name       string
		result     string
		total      int
		matched    int
		mismatches []string
		noResult   bool
	}{
		{
			name:    "identical",
			result:  expected,
			total:   8,
			matched: 8,
		},
		{
			name: "loose matches",
			result: `{"title": "Receipt", "notes": "other text", "extra": 1,
				"items": [{"name": "café au lait", "price": 3.5}, {"name": "BUN", "price": 1.205}],
				"tags": ["b", "a", "b"], "people": [{"name": "alan turing"}, {"name": "ADA LOVELACE"}]}`,
			total:   8,
			matched: 8,
		},
		{
			name: "every mode differs",
			result: `{"title": "receipt", "missing": 0,
				"items": [{"name": "Tea", "price": 3.51}, {"name": "Bun", "price": 1.25}],
				"tags": ["a", "b"], "people": [{"name": "Ada Lovelace"}]}`,
			total:      8,
			matched:    1,
			mismatches: []string{"/items/0/name", "/items/0/price", "/items/1/price", "/missing", "/people", "/tags", "/title"},
		},
		{
			name:       "missing and extra elements",
			result:     `{"title": "Receipt", "items": [{"name": "Café au lait", "price": 3.5}, {"name": "Bun", "price": 1.2}, {"name": "X", "price": 1}], "tags": ["a","b","b"], "people": [{"name": "Ada Lovelace"}, {"name": "Alan Turing"}]}`,
			total:      9,
			matched:    8,
			mismatches: []string{"/items/2"},
		},
		{
			name:       "array missing entirely",
			result:     `{"title": "Receipt", "tags": ["a","b","b"], "people": [{"name": "Ada Lovelace"}, {"name": "Alan Turing"}]}`,
			total:      8,
			matched:    4,
			mismatches: []string{"/items/0/name", "/items/0/price", "/items/1/name", "/items/1/price"},
		},
		{name: "null result", result: `null`, total: 8, noResult: true},
		{name: "no result", result: ``, total: 8, noResult: true},
		{name: "not JSON", result: `{"title":`, total: 8, noResult: true},
	}
	r := mustRules(t, rules)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := r.Compare(json.RawMessage(expected), json.RawMessage(tt.result))
			if err != nil {
				t.Fatal(err)
			}
			if f.Total != tt.total || f.Matched != tt.matched || f.NoResult != tt.noResult {
				t.Errorf("Compare() = total %d matched %d noResult %t, want %d %d %t (mismatches %v)",
					f.Total, f.Matched, f.NoResult, tt.total, tt.matched, tt.noResult, f.Mismatches)
			}
			if !reflect.DeepEqual(f.Mismatches, tt.mismatches) {
				t.Errorf("Mismatches = %q, want %q", f.Mismatches, tt.mismatches)
			}
			if f.Correct() != (tt.matched == tt.total && !tt.noResult) {
				t.Errorf("Correct() = %t", f.Correct())
			}
		})
	}
}

func TestRules_Compare_defaultNormalizedAndRoot(t *testing.T) {
	t.Parallel()
	r := mustRules(t, "default: normalized\n")
	f, err := r.Compare(json.RawMessage(`{"a": "Hello  World", "b": [1, 2], "c": {}}`), json.RawMessage(`{"a": "hello world.", "b": [1, 2], "c": {}}`))
	if err != nil || !f.Correct() || f.Total != 4 {
		t.Errorf("Compare() = %+v, %v; want 4 fields all matched", f, err)
	}

	root := mustRules(t, `rules: [{pointer: "", mode: ignore}]`)
	if f, _ := root.Compare(json.RawMessage(`{"a": 1}`), json.RawMessage(`{"a": 2}`)); f.Total != 0 || !f.Correct() {
		t.Errorf("ignore at the root: %+v, want nothing compared", f)
	}
	var none eval.Rules
	if f, _ := none.Compare(json.RawMessage(`{"a/b": {"~": 1}}`), json.RawMessage(`{}`)); !reflect.DeepEqual(f.Mismatches, []string{"/a~1b/~0"}) {
		t.Errorf("escaped pointer: %q", f.Mismatches)
	}
}

func TestRules_Compare_invalidExpected(t *testing.T) {
	t.Parallel()
	var none eval.Rules
	if _, err := none.Compare(json.RawMessage(`{`), json.RawMessage(`{}`)); err == nil {
		t.Error("Compare(invalid expected) succeeded, want an error")
	}
}

func TestParseRules_errors(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"unknown mode":            `rules: [{pointer: /a, mode: fuzzy}]`,
		"unknown key":             `rules: [{pointer: /a, mode: exact, weight: 2}]`,
		"bad pointer":             `rules: [{pointer: a, mode: exact}]`,
		"negative tolerance":      `rules: [{pointer: /a, mode: numeric, tolerance: -1}]`,
		"tolerance on exact":      `rules: [{pointer: /a, mode: exact, tolerance: 1}]`,
		"elements on numeric":     `rules: [{pointer: /a, mode: numeric, elements: exact}]`,
		"bad elements":            `rules: [{pointer: /a, mode: set, elements: numeric}]`,
		"bad default":             `default: set`,
		"top-level unknown field": `mode: exact`,
	}
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := eval.ParseRules([]byte(yaml)); err == nil {
				t.Errorf("ParseRules(%q) succeeded, want an error", yaml)
			}
		})
	}
	if r, err := eval.ParseRules(nil); err != nil || r.Default != eval.ModeExact {
		t.Errorf("ParseRules(empty) = %+v, %v; want default exact", r, err)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"  Hello\n\tWorld. ": "hello world",
		"It’s “quoted” – ok": `it's "quoted" - ok`,
		"A\u00a0B":           "a b",
		"":                   "",
	}
	for in, want := range tests {
		if got := eval.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(eval.Normalize("x."), ".") {
		t.Error("trailing full stop kept")
	}
}

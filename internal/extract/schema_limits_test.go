package extract

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// refChain is a schema whose n definitions each apply the next one twice at the same
// location: 2^n evaluations for any instance, in a few hundred bytes.
func refChain(n int) string {
	var b strings.Builder
	b.WriteString(`{"$ref":"#/$defs/d0","$defs":{`)
	for i := range n {
		fmt.Fprintf(&b, `"d%d":{"allOf":[{"$ref":"#/$defs/d%d"},{"$ref":"#/$defs/d%d"}]},`, i, i+1, i+1)
	}
	fmt.Fprintf(&b, `"d%d":{"type":"object"}}}`, n)
	return b.String()
}

// nested returns a value nested depth levels deep under the key, around leaf.
func nested(key string, depth int, leaf string) string {
	return strings.Repeat(`{"`+key+`":`, depth) + leaf + strings.Repeat(`}`, depth)
}

// TestCompileSchema_bounds: schemas that made the validator panic, take minutes or grow
// exponentially are refused as invalid, quickly.
func TestCompileSchema_bounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		schema  string
		invalid bool
	}{
		{name: "shared references, small", schema: refChain(8)},
		{name: "shared references, exponential", schema: refChain(40), invalid: true},
		{name: "nesting at the limit", schema: nested("not", maxSchemaDepth-1, `{}`)},
		{name: "nesting over the limit", schema: nested("not", maxSchemaDepth, `{}`), invalid: true},
		{name: "nesting of thousands", schema: nested("items", 9000, `{}`), invalid: true},
		{name: "huge exponent in multipleOf", schema: `{"multipleOf": 1e-9999999}`, invalid: true},
		{name: "large exponent in minimum", schema: `{"minimum": 1e1001}`, invalid: true},
		{name: "exponent at the limit", schema: `{"minimum": 1e1000}`},
		{name: "long number in enum", schema: `{"enum": [` + strings.Repeat("9", maxNumberDigits+1) + `]}`, invalid: true},
		{name: "recursive tree", schema: `{"$defs":{"node":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/node"}}}}},"$ref":"#/$defs/node"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			_, err := CompileSchema([]byte(tt.schema))
			if tt.invalid != (err != nil) {
				t.Fatalf("CompileSchema: err %v, want invalid %v", err, tt.invalid)
			}
			if err != nil && !errors.Is(err, ErrInvalidSchema) {
				t.Errorf("err %v does not wrap ErrInvalidSchema", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("took %v", d)
			}
		})
	}
}

// TestSchema_Validate_bounds: candidates the validator cannot check cheaply get feedback,
// never a panic, a long run or acceptance.
func TestSchema_Validate_bounds(t *testing.T) {
	t.Parallel()
	const (
		tree = `{"$defs":{"node":{"type":"object","properties":{"c":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`
		// Every level applies the root twice to the same member: 2^depth evaluations.
		fanOut = `{"allOf":[{"$ref":"#/$defs/r"},{"$ref":"#/$defs/r"}],"$defs":{"r":{"type":"object","properties":{"c":{"$ref":"#"}}}}}`
	)
	tests := []struct {
		name, schema, candidate string
		wantFeedback            string // a substring of the single entry; "" for valid
	}{
		{name: "number at the limit", schema: `{"type":"number","minimum":0}`, candidate: `1e1000`},
		{name: "huge exponent", schema: `{"type":"number","minimum":0}`, candidate: `1e9999999`, wantFeedback: "exponent"},
		{name: "large exponent inside", schema: `{"type":"object"}`, candidate: `{"a":[1,2,1e-5000]}`, wantFeedback: "exponent"},
		{name: "long number", schema: `{"type":"integer"}`, candidate: strings.Repeat("7", maxNumberDigits+1), wantFeedback: "digits"},
		{name: "deep recursive tree", schema: tree, candidate: nested("c", 500, `{}`)},
		{name: "recursion with fan-out, shallow", schema: fanOut, candidate: nested("c", 4, `{}`)},
		{name: "recursion with fan-out, deep", schema: fanOut, candidate: nested("c", 60, `{}`), wantFeedback: "nested too deeply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := CompileSchema([]byte(tt.schema))
			if err != nil {
				t.Fatalf("CompileSchema: %v", err)
			}
			start := time.Now()
			got := s.Validate([]byte(tt.candidate))
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("Validate took %v", d)
			}
			if tt.wantFeedback == "" {
				if len(got) != 0 {
					t.Errorf("Validate = %+v, want valid", got)
				}
				return
			}
			if len(got) != 1 || got[0].Pointer != "" || got[0].Source != SourceSchema || !strings.Contains(got[0].Message, tt.wantFeedback) {
				t.Errorf("Validate = %+v, want one schema entry on the result containing %q", got, tt.wantFeedback)
			}
		})
	}
}

// TestCompileSchema_corpusWithinBounds: every schema of the corpora stays far inside the
// expansion bound, so the bound refuses only constructed schemas.
func TestCompileSchema_corpusWithinBounds(t *testing.T) {
	t.Parallel()
	s := travelSchema(t)
	n, recursive, ok := expansion(s.compiled, -1, maxSchemaExpansion)
	if !ok || recursive || n*10 > maxSchemaExpansion {
		t.Errorf("TravelInbox schema expands to %d (recursive %v), want a tenth of %d at most", n, recursive, maxSchemaExpansion)
	}
}

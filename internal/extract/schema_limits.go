package extract

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Bounds on what a caller's schema and a candidate may make the validator do (security
// review v1.0, docs/security.md "Hardening review"). The validator has no context and no
// work limit of its own, so these checks run before it.
const (
	// maxSchemaDepth is the deepest JSON nesting a schema may have. The library compiles
	// recursively and its cost grows faster than linearly with depth: a schema nested
	// 9000 levels took minutes to compile.
	maxSchemaDepth = 64
	// maxSchemaExpansion bounds the schema evaluations one validation may need, counted
	// over the compiled schema with every reference expanded. References that share a
	// target ({"allOf": [{"$ref": "#/$defs/a"}, {"$ref": "#/$defs/a"}]} chained n times)
	// make validation exponential in n without making the schema large.
	maxSchemaExpansion = 10_000
	// maxNumberDigits and maxNumberExponent bound a JSON number literal in a schema or a
	// candidate. The validator compares numbers as exact rationals: 1e9999999 made it
	// panic and 1e999999 cost tens of milliseconds per comparison.
	maxNumberDigits   = 1000
	maxNumberExponent = 1000
)

// checkJSONBounds reports whether a decoded JSON value (from jsonschema.UnmarshalJSON,
// numbers as json.Number) has no number outside the literal bounds and no nesting deeper
// than maxDepth (0: any depth), and returns its depth: 0 for a scalar.
func checkJSONBounds(v any, maxDepth int) (depth int, ok bool) {
	type frame struct {
		v     any
		depth int
	}
	stack := []frame{{v, 0}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch node := f.v.(type) {
		case json.Number:
			if !numberInBounds(string(node)) {
				return 0, false
			}
		case map[string]any:
			if maxDepth > 0 && f.depth+1 > maxDepth {
				return 0, false
			}
			depth = max(depth, f.depth+1)
			for _, c := range node {
				stack = append(stack, frame{c, f.depth + 1})
			}
		case []any:
			if maxDepth > 0 && f.depth+1 > maxDepth {
				return 0, false
			}
			depth = max(depth, f.depth+1)
			for _, c := range node {
				stack = append(stack, frame{c, f.depth + 1})
			}
		}
	}
	return depth, true
}

// numberInBounds reports whether a JSON number literal has at most maxNumberDigits
// mantissa digits and an exponent of at most maxNumberExponent in magnitude.
func numberInBounds(lit string) bool {
	mantissa, exp, hasExp := strings.Cut(strings.ToLower(lit), "e")
	digits := len(mantissa) - strings.Count(mantissa, "-") - strings.Count(mantissa, ".")
	if digits > maxNumberDigits {
		return false
	}
	if !hasExp {
		return true
	}
	e, err := strconv.Atoi(strings.TrimPrefix(exp, "+"))
	return err == nil && e >= -maxNumberExponent && e <= maxNumberExponent
}

// expansion counts schema evaluations in the compiled schema s with every reference
// expanded, stopping as soon as the count exceeds limit. With depth < 0 a reference
// back to a schema already being expanded is not followed, so the count bounds one
// instance location's work for a schema without recursion; recursive reports whether
// such a reference was met. With depth >= 0 recursion is followed, but only through as
// many keywords that descend into the instance (properties, items, ...) as depth
// allows: the count then bounds the work on an instance that deep.
func expansion(s *jsonschema.Schema, depth, limit int) (n int, recursive, ok bool) {
	w := &expander{limit: limit, onStack: map[expandKey]bool{}, dynamic: map[string][]*jsonschema.Schema{}}
	w.collectAnchors(s, map[*jsonschema.Schema]bool{})
	w.walk(s, depth)
	return w.n, w.recursive, w.n <= w.limit
}

type expandKey struct {
	s     *jsonschema.Schema
	depth int
}

type expander struct {
	n, limit  int
	recursive bool
	onStack   map[expandKey]bool
	// Targets a $dynamicRef or $recursiveRef may resolve to at validation time: every
	// schema with that $dynamicAnchor, or with $recursiveAnchor: true.
	dynamic         map[string][]*jsonschema.Schema
	recursiveAnchor []*jsonschema.Schema
}

func (w *expander) walk(s *jsonschema.Schema, depth int) {
	if s == nil || w.n > w.limit {
		return
	}
	key := expandKey{s, depth}
	if depth < 0 {
		key.depth = -1
	}
	if w.onStack[key] {
		w.recursive = true // a cycle at one instance location the library reports as RefCycle when depth >= 0
		return
	}
	w.n++
	w.onStack[key] = true
	defer delete(w.onStack, key)
	here, below := applicators(s)
	for _, c := range here {
		w.walk(c, depth)
	}
	if s.DynamicRef != nil && s.DynamicRef.Anchor != "" {
		for _, c := range w.dynamic[s.DynamicRef.Anchor] {
			w.walk(c, depth)
		}
	}
	if s.RecursiveRef != nil {
		for _, c := range w.recursiveAnchor {
			w.walk(c, depth)
		}
	}
	if depth == 0 {
		return
	}
	next := depth - 1
	if depth < 0 {
		next = depth
	}
	for _, c := range below {
		w.walk(c, next)
	}
}

// collectAnchors records every schema reachable from s that a dynamic reference may
// resolve to.
func (w *expander) collectAnchors(s *jsonschema.Schema, seen map[*jsonschema.Schema]bool) {
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	if s.DynamicAnchor != "" {
		w.dynamic[s.DynamicAnchor] = append(w.dynamic[s.DynamicAnchor], s)
	}
	if s.RecursiveAnchor {
		w.recursiveAnchor = append(w.recursiveAnchor, s)
	}
	here, below := applicators(s)
	for _, c := range append(here, below...) {
		w.collectAnchors(c, seen)
	}
}

// applicators returns the subschemas of s applied to the same instance location (here)
// and to the instance's members or items (below).
func applicators(s *jsonschema.Schema) (here, below []*jsonschema.Schema) {
	here = append(here, s.Ref, s.RecursiveRef, s.Not, s.If, s.Then, s.Else)
	if s.DynamicRef != nil {
		here = append(here, s.DynamicRef.Ref)
	}
	here = append(here, s.AllOf...)
	here = append(here, s.AnyOf...)
	here = append(here, s.OneOf...)
	for _, c := range s.DependentSchemas {
		here = append(here, c)
	}
	for _, d := range s.Dependencies {
		if c, ok := d.(*jsonschema.Schema); ok {
			here = append(here, c)
		}
	}
	below = append(below, s.PropertyNames, s.UnevaluatedProperties, s.Contains, s.Items2020,
		s.UnevaluatedItems, s.ContentSchema)
	for _, c := range s.Properties {
		below = append(below, c)
	}
	for _, c := range s.PatternProperties {
		below = append(below, c)
	}
	below = append(below, s.PrefixItems...)
	for _, v := range []any{s.AdditionalProperties, s.Items, s.AdditionalItems} {
		switch c := v.(type) {
		case *jsonschema.Schema:
			below = append(below, c)
		case []*jsonschema.Schema:
			below = append(below, c...)
		}
	}
	return here, below
}

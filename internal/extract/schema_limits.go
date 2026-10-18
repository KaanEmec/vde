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
// than maxDepth (0: any depth).
func checkJSONBounds(v any, maxDepth int) bool {
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
				return false
			}
		case map[string]any:
			if maxDepth > 0 && f.depth+1 > maxDepth {
				return false
			}
			for _, c := range node {
				stack = append(stack, frame{c, f.depth + 1})
			}
		case []any:
			if maxDepth > 0 && f.depth+1 > maxDepth {
				return false
			}
			for _, c := range node {
				stack = append(stack, frame{c, f.depth + 1})
			}
		}
	}
	return true
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
// expanded, stopping as soon as the count exceeds limit. A reference back to a schema
// already being expanded is not followed, so the count bounds one instance location's
// work for a schema without recursion; recursive reports whether such a reference was
// met (instanceExpansion then bounds each validation).
func expansion(s *jsonschema.Schema, limit int) (n int, recursive, ok bool) {
	w := newExpander(s, limit)
	w.walk(s, nil)
	return w.n, w.recursive, !w.over
}

// instanceExpansion reports whether validating inst (decoded by jsonschema.UnmarshalJSON)
// against s evaluates at most limit schemas at every location of inst. It follows the
// recursion of s only into the members and items inst has, as the validator does, so
// the count grows with what the candidate holds and not with the schema's branching:
// a schema that applies itself twice to one member (2^depth evaluations) is refused, a
// tree with two recursive properties of which the candidate uses one is not.
func instanceExpansion(s *jsonschema.Schema, inst any, limit int) bool {
	w := newExpander(s, limit)
	w.walk(s, newLocation(inst))
	return !w.over
}

func newExpander(s *jsonschema.Schema, limit int) *expander {
	w := &expander{limit: limit, onStack: map[expandKey]bool{}, dynamic: map[string][]*jsonschema.Schema{}}
	w.collectAnchors(s, map[*jsonschema.Schema]bool{})
	return w
}

// location is one location of the instance being validated and the evaluations counted
// at it.
type location struct {
	n       int
	members map[string]*location // an object's members
	names   map[string]*location // an object's member names, which propertyNames checks
	items   []*location          // an array's items
}

func newLocation(v any) *location {
	l := &location{}
	switch v := v.(type) {
	case map[string]any:
		l.members, l.names = make(map[string]*location, len(v)), make(map[string]*location, len(v))
		for name, c := range v {
			l.members[name], l.names[name] = newLocation(c), &location{}
		}
	case []any:
		l.items = make([]*location, len(v))
		for i, c := range v {
			l.items[i] = newLocation(c)
		}
	}
	return l
}

type expandKey struct {
	s  *jsonschema.Schema
	at *location
}

type expander struct {
	n, limit  int
	over      bool
	recursive bool
	onStack   map[expandKey]bool
	// Targets a $dynamicRef or $recursiveRef may resolve to at validation time: every
	// schema with that $dynamicAnchor, or with $recursiveAnchor: true.
	dynamic         map[string][]*jsonschema.Schema
	recursiveAnchor []*jsonschema.Schema
}

// walk counts s and what it applies. With at nil (no instance) the subschemas for
// members and items are walked once each, a cycle cut; with an instance location they
// are walked for the members and items at has, and the count is at's.
func (w *expander) walk(s *jsonschema.Schema, at *location) {
	if s == nil || w.over {
		return
	}
	key := expandKey{s, at}
	if w.onStack[key] {
		w.recursive = true // a cycle at one instance location: the library reports RefCycle
		return
	}
	count := &w.n
	if at != nil {
		count = &at.n
	}
	*count++
	if *count > w.limit {
		w.over = true
		return
	}
	w.onStack[key] = true
	defer delete(w.onStack, key)
	here, below := applicators(s)
	for _, c := range here {
		w.walk(c, at)
	}
	if s.DynamicRef != nil && s.DynamicRef.Anchor != "" {
		for _, c := range w.dynamic[s.DynamicRef.Anchor] {
			w.walk(c, at)
		}
	}
	if s.RecursiveRef != nil {
		for _, c := range w.recursiveAnchor {
			w.walk(c, at)
		}
	}
	if at == nil {
		for _, c := range below {
			w.walk(c, nil)
		}
		return
	}
	w.descend(s, at)
}

// descend walks the subschemas s applies to the members and items at has, at least as
// many as the validator applies (unevaluatedProperties and unevaluatedItems count for
// every member or item s itself does not cover). contentSchema is never applied: the
// compiler does not enable content assertions.
func (w *expander) descend(s *jsonschema.Schema, at *location) {
	for name, child := range at.members {
		matched := false
		if c, ok := s.Properties[name]; ok {
			w.walk(c, child)
			matched = true
		}
		for re, c := range s.PatternProperties {
			if re.MatchString(name) {
				w.walk(c, child)
				matched = true
			}
		}
		if !matched {
			if c, ok := s.AdditionalProperties.(*jsonschema.Schema); ok {
				w.walk(c, child)
			}
			w.walk(s.UnevaluatedProperties, child)
		}
		w.walk(s.PropertyNames, at.names[name])
	}
	for i, child := range at.items {
		covered := true
		switch {
		case i < len(s.PrefixItems):
			w.walk(s.PrefixItems[i], child)
		case s.Items2020 != nil:
			w.walk(s.Items2020, child)
		default:
			covered = false
		}
		switch items := s.Items.(type) { // drafts before 2020-12
		case *jsonschema.Schema:
			w.walk(items, child)
			covered = true
		case []*jsonschema.Schema:
			if i < len(items) {
				w.walk(items[i], child)
				covered = true
			} else if c, ok := s.AdditionalItems.(*jsonschema.Schema); ok {
				w.walk(c, child)
				covered = true
			}
		}
		w.walk(s.Contains, child)
		if !covered {
			w.walk(s.UnevaluatedItems, child)
		}
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

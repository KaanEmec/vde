package ground

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Rule is how a leaf is treated, from the schema annotation x-vde-grounding.
type Rule uint8

// Rules. RuleNone means no annotation applies: the leaf is checked, and it is critical
// unless some leaf is annotated critical.
const (
	RuleNone     Rule = iota
	RuleCheck         // "check": checked; overrides an inherited derived or skip
	RuleDerived       // "derived": an allowed derivation (city, IATA code, currency), not checked
	RuleSkip          // "skip": never checked (notes, titles, confidence)
	RuleCritical      // "critical": checked, and must be grounded for the auto gate
)

// Annotation is the schema keyword that carries a Rule.
const Annotation = "x-vde-grounding"

var ruleNames = [...]string{RuleNone: "", RuleCheck: "check", RuleDerived: "derived", RuleSkip: "skip", RuleCritical: "critical"}

// ErrUnknownRule is returned for an x-vde-grounding value that is not check, derived,
// skip or critical.
var ErrUnknownRule = errors.New("ground: unknown " + Annotation + " value")

// ParseRule parses an annotation value.
func ParseRule(s string) (Rule, error) {
	for r, n := range ruleNames {
		if r != int(RuleNone) && n == s {
			return Rule(r), nil
		}
	}
	return RuleNone, ErrUnknownRule
}

// Rules resolves a leaf's location (its unescaped RFC 6901 segments, array indices in
// decimal) to the Rule that applies there and the schema's enum and const values at that
// location. String enum values are given as the string; other values as their JSON text.
// A nil Rules is schema-less mode: every leaf is checked and critical.
type Rules func(location []string) (rule Rule, enum []string)

// defaultSkip are property names skipped when a schema carries no x-vde-grounding
// annotation at all: they hold the model's own words or scores, never document values.
// A schema with any annotation states its intent and gets no defaults.
var defaultSkip = map[string]bool{"confidence": true, "notes": true}

// RulesFromSchema reads x-vde-grounding, enum and const from a raw JSON Schema and
// returns the Rules for it. A location is resolved through properties,
// additionalProperties, prefixItems, items (array form included), local $ref ("#",
// "#/$defs/...", "#/definitions/...") and every allOf, anyOf and oneOf branch. An
// annotation applies to every leaf below it unless a deeper annotation overrides it; at
// one level the referencing schema wins over its $ref target and branches. When the
// schema has no annotation anywhere, leaves under a property named confidence or notes
// are skipped. annotated reports whether the schema carries any x-vde-grounding
// annotation: only such a schema states which values may be derived, so the loop makes
// the grounding repair under the always policy only for it (ADR 0009, amendment). An
// annotation value other than check, derived, skip or critical is an error wrapping
// ErrUnknownRule.
func RulesFromSchema(schema []byte) (rules Rules, annotated bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, false, fmt.Errorf("ground: schema: %w", err)
	}
	if annotated, err = checkAnnotations(root, ""); err != nil {
		return nil, false, err
	}
	r := &resolver{root: root, annotated: annotated}
	return r.rules, annotated, nil
}

// checkAnnotations validates every x-vde-grounding value below v and reports whether
// there is one.
func checkAnnotations(v any, ptr string) (bool, error) {
	found := false
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if k == Annotation {
				s, _ := child.(string)
				if _, err := ParseRule(s); err != nil {
					return false, fmt.Errorf("%w at %s", err, ptr+"/"+escapePointer(k))
				}
				found = true
				continue
			}
			f, err := checkAnnotations(child, ptr+"/"+escapePointer(k))
			if err != nil {
				return false, err
			}
			found = found || f
		}
	case []any:
		for i, child := range v {
			f, err := checkAnnotations(child, ptr+"/"+strconv.Itoa(i))
			if err != nil {
				return false, err
			}
			found = found || f
		}
	}
	return found, nil
}

type resolver struct {
	root      any
	annotated bool
}

// maxExpand bounds $ref and combinator expansion, so recursive schemas terminate.
const maxExpand = 16

func (r *resolver) rules(location []string) (Rule, []string) {
	nodes := r.expand(nil, r.root, 0, map[uintptr]bool{})
	rule := ruleAt(nodes)
	for _, seg := range location {
		var next []map[string]any
		seen := map[uintptr]bool{}
		for _, n := range nodes {
			if c, ok := child(n, seg); ok {
				next = r.expand(next, c, 0, seen)
			}
		}
		nodes = next
		if rl := ruleAt(nodes); rl != RuleNone {
			rule = rl
		}
	}
	if rule == RuleNone && !r.annotated && defaultSkip[lastProperty(location)] {
		rule = RuleSkip
	}
	return rule, enumAt(nodes)
}

// expand appends the schema object v, its $ref target and its allOf, anyOf and oneOf
// branches, in that order, each schema object once.
func (r *resolver) expand(out []map[string]any, v any, depth int, seen map[uintptr]bool) []map[string]any {
	m, ok := v.(map[string]any)
	if !ok || depth > maxExpand {
		return out
	}
	id := reflect.ValueOf(m).Pointer()
	if seen[id] {
		return out
	}
	seen[id] = true
	out = append(out, m)
	if ref, ok := m["$ref"].(string); ok {
		if target, ok := r.ref(ref); ok {
			out = r.expand(out, target, depth+1, seen)
		}
	}
	for _, kw := range [...]string{"allOf", "anyOf", "oneOf"} {
		branches, _ := m[kw].([]any)
		for _, b := range branches {
			out = r.expand(out, b, depth+1, seen)
		}
	}
	return out
}

// ref resolves a local reference: "#" or a JSON pointer fragment.
func (r *resolver) ref(ref string) (any, bool) {
	if ref == "#" {
		return r.root, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	v := r.root
	for _, seg := range strings.Split(ref[2:], "/") {
		seg = UnescapeToken(seg)
		var ok bool
		if v, ok = jsonChild(v, seg); !ok {
			return nil, false
		}
	}
	return v, true
}

// child is the subschema of the schema object v for one location segment.
func child(v any, seg string) (any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if props, ok := m["properties"].(map[string]any); ok {
		if c, ok := props[seg]; ok {
			return c, true
		}
	}
	if i, err := strconv.Atoi(seg); err == nil && i >= 0 {
		if prefix, ok := m["prefixItems"].([]any); ok && i < len(prefix) {
			return prefix[i], true
		}
		switch items := m["items"].(type) {
		case map[string]any:
			return items, true
		case []any:
			if i < len(items) {
				return items[i], true
			}
		}
	}
	if ap, ok := m["additionalProperties"].(map[string]any); ok {
		return ap, true
	}
	return nil, false
}

// jsonChild is the plain JSON child of v, for walking a $ref pointer.
func jsonChild(v any, seg string) (any, bool) {
	switch v := v.(type) {
	case map[string]any:
		c, ok := v[seg]
		return c, ok
	case []any:
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(v) {
			return v[i], true
		}
	}
	return nil, false
}

func ruleAt(nodes []map[string]any) Rule {
	for _, n := range nodes {
		if s, ok := n[Annotation].(string); ok {
			if rl, err := ParseRule(s); err == nil {
				return rl
			}
		}
	}
	return RuleNone
}

func enumAt(nodes []map[string]any) []string {
	var out []string
	add := func(v any) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		} else if b, err := json.Marshal(v); err == nil {
			out = append(out, string(b))
		}
	}
	for _, n := range nodes {
		if e, ok := n["enum"].([]any); ok {
			for _, v := range e {
				add(v)
			}
		}
		if c, ok := n["const"]; ok {
			add(c)
		}
	}
	return out
}

// lastProperty is the last segment of location that is not an array index.
func lastProperty(location []string) string {
	for i := len(location) - 1; i >= 0; i-- {
		if !allDigits(location[i]) {
			return location[i]
		}
	}
	return ""
}

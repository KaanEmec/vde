package extract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/kaanemec/vde/internal/ground"
)

// ErrInvalidSchema: the schema is not valid JSON Schema (draft 2020-12 unless it names
// another draft), or it references a resource outside itself. It maps to the contract's
// 422 invalid_schema.
var ErrInvalidSchema = errors.New("extract: invalid schema")

// schemaURL is the location the caller's schema is compiled under. A schema without $id
// resolves its relative references against it; the .invalid domain never resolves and
// noLoader refuses to load anything anyway.
const schemaURL = "https://vde.invalid/schema.json"

// maxSchemaFeedback caps the schema violations reported per candidate; a summary entry
// follows when there are more.
const maxSchemaFeedback = 50

// validatorModule is the JSON Schema library, whose version is part of service_version.
const validatorModule = "github.com/santhosh-tekuri/jsonschema/v6"

// Schema is a compiled caller schema. It is safe for concurrent use.
type Schema struct {
	raw        []byte // as the caller sent it; given to both roles
	doc        any    // the decoded schema, for descriptions in messages
	base       string // the root resource's location, without fragment
	compiled   *jsonschema.Schema
	allowArray bool         // the top level may be an array
	rules      ground.Rules // x-vde-grounding, enum and const per location (ADR 0009)
	annotated  bool         // some x-vde-grounding annotation: the repair runs under always
	recursive  bool         // a reference cycle: Validate bounds the expansion by the candidate's depth
}

// noLoader refuses every resource the schema does not contain itself: remote $refs and
// files are never fetched.
type noLoader struct{}

func (noLoader) Load(string) (any, error) {
	return nil, errors.New("references outside the schema are not allowed")
}

// CompileSchema compiles a JSON Schema, draft 2020-12 by default, with format assertions
// on, and reads its grounding annotations (x-vde-grounding, ADR 0009). Any error,
// including an unknown annotation value, wraps ErrInvalidSchema. A schema over the
// bounds of schema_limits.go (nesting, number literals, expanded size) is invalid too,
// and a panic in the library is reported as an invalid schema.
func CompileSchema(data []byte) (_ *Schema, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: the validator failed on it", ErrInvalidSchema)
		}
	}()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: not JSON: %w", ErrInvalidSchema, err)
	}
	if _, ok := checkJSONBounds(doc, maxSchemaDepth); !ok {
		return nil, fmt.Errorf("%w: nested deeper than %d levels or a number literal over %d digits or exponent %d",
			ErrInvalidSchema, maxSchemaDepth, maxNumberDigits, maxNumberExponent)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(noLoader{})
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
	}
	compiled, err := c.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
	}
	_, recursive, ok := expansion(compiled, -1, maxSchemaExpansion)
	if !ok {
		return nil, fmt.Errorf("%w: its references expand to more than %d subschemas", ErrInvalidSchema, maxSchemaExpansion)
	}
	rules, annotated, err := ground.RulesFromSchema(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
	}
	base, _, _ := strings.Cut(compiled.Location, "#")
	return &Schema{
		raw:        bytes.Clone(data),
		doc:        doc,
		base:       base,
		compiled:   compiled,
		allowArray: compiled.Types == nil || slices.Contains(compiled.Types.ToStrings(), "array"),
		rules:      rules,
		annotated:  annotated,
		recursive:  recursive,
	}, nil
}

// GroundingRules returns the grounding rules read from the schema; nil (every leaf
// checked and critical) for a nil schema.
func (s *Schema) GroundingRules() ground.Rules {
	if s == nil {
		return nil
	}
	return s.rules
}

// GroundingAnnotated reports whether the schema carries any x-vde-grounding annotation;
// false for a nil schema.
func (s *Schema) GroundingAnnotated() bool {
	return s != nil && s.annotated
}

// Validate returns one feedback entry per violation of the schema by candidate, each with
// a pointer into the candidate and a message naming the keyword and the expected form,
// sorted by pointer, at most maxSchemaFeedback plus a summary. It returns nil when the
// candidate is valid. A candidate the validator cannot check within the bounds of
// schema_limits.go gets one entry on the whole result, never acceptance.
func (s *Schema) Validate(candidate json.RawMessage) (out []Feedback) {
	defer func() {
		if r := recover(); r != nil {
			out = []Feedback{schemaFeedback("", "The result could not be validated against the schema.")}
		}
	}()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(candidate))
	if err != nil {
		return []Feedback{schemaFeedback("", "The result is not valid JSON.")}
	}
	depth, ok := checkJSONBounds(inst, 0)
	if !ok {
		return []Feedback{schemaFeedback("", fmt.Sprintf(
			"The result has a number with more than %d digits or an exponent beyond %d; write numbers plainly.",
			maxNumberDigits, maxNumberExponent))}
	}
	if s.recursive {
		if _, _, ok := expansion(s.compiled, depth, maxSchemaExpansion); !ok {
			return []Feedback{schemaFeedback("", "The result is nested too deeply to be validated against the schema.")}
		}
	}
	err = s.compiled.Validate(inst)
	if err == nil {
		return nil
	}
	ve, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return []Feedback{schemaFeedback("", "The result could not be validated against the schema.")}
	}
	f := &flattener{s: s, inst: inst}
	f.flatten(ve, nil)
	out = f.out
	if len(out) == 0 { // never let a failed validation pass as valid
		out = []Feedback{schemaFeedback("", "The result does not validate against the schema.")}
	}
	slices.SortStableFunc(out, func(a, b Feedback) int { return strings.Compare(a.Pointer, b.Pointer) })
	if len(out) > maxSchemaFeedback {
		more := len(out) - maxSchemaFeedback
		out = append(out[:maxSchemaFeedback:maxSchemaFeedback],
			schemaFeedback("", fmt.Sprintf("%d more schema violations are not listed.", more)))
	}
	return out
}

var printer = message.NewPrinter(language.English)

// flattener collects the leaf violations of a validation error tree, once each.
type flattener struct {
	s    *Schema
	inst any // the candidate, decoded
	out  []Feedback
	seen map[Feedback]bool
}

func (f *flattener) add(fb Feedback) {
	if f.seen == nil {
		f.seen = map[Feedback]bool{}
	}
	if !f.seen[fb] {
		f.seen[fb] = true
		f.out = append(f.out, fb)
	}
}

// flatten adds the leaf violations under e. Groups (the whole schema, $ref, allOf,
// several keywords failing at one location) are descended; oneOf and anyOf use the
// discriminator heuristic of alternatives. within is the instance location of the
// nearest enclosing error.
func (f *flattener) flatten(e *jsonschema.ValidationError, within []string) {
	s, loc := f.s, e.InstanceLocation
	switch k := e.ErrorKind.(type) {
	case *kind.Schema, *kind.Group, *kind.Reference, *kind.AllOf:
		for _, c := range e.Causes {
			f.flatten(c, loc)
		}
	case *kind.OneOf:
		if len(k.Subschemas) > 1 {
			f.add(s.leaf(e, "matches more than one of the alternatives", "oneOf"))
			return
		}
		f.alternatives(e, "oneOf")
	case *kind.AnyOf:
		f.alternatives(e, "anyOf")
	case *kind.Required:
		for _, name := range k.Missing {
			f.add(s.leaf(e, "missing required property "+quote(name), "required"))
		}
	case *kind.AdditionalProperties:
		allowed := s.subschemaKeys(e.SchemaURL, "properties")
		for _, name := range k.Properties {
			msg := fmt.Sprintf("%s: property is not allowed (keyword additionalProperties)", label(name))
			if len(allowed) > 0 {
				msg += "; expected only " + strings.Join(allowed, ", ")
			}
			f.add(schemaFeedback(pointer(append(slices.Clip(loc), name)), msg))
		}
	case *kind.PropertyNames:
		msg := fmt.Sprintf("%s: property name is not allowed (keyword propertyNames)", label(k.Property))
		if names := s.enumValues(e.SchemaURL); len(names) > 0 {
			msg += "; expected one of " + strings.Join(names, ", ")
		}
		// The library reports this error with an instance location that later
		// validation overwrites; only its length is reliable. Point at every object at
		// that depth, inside the enclosing error's location, holding the property.
		for _, obj := range objectsWith(f.inst, nil, within, len(loc), k.Property) {
			f.add(schemaFeedback(pointer(append(obj, k.Property)), msg))
		}
	default:
		keyword := "schema"
		if kp := k.KeywordPath(); len(kp) > 0 {
			keyword = kp[0]
		}
		f.add(s.leaf(e, k.LocalizedString(printer), keyword))
	}
}

// objectsWith returns the locations of the objects at depth below v (at loc), inside
// the location within, that have the property name.
func objectsWith(v any, loc, within []string, depth int, name string) [][]string {
	if n := min(len(loc), len(within)); !slices.Equal(loc[:n], within[:n]) {
		return nil
	}
	if len(loc) == depth {
		if obj, ok := v.(map[string]any); ok {
			if _, ok := obj[name]; ok {
				return [][]string{loc}
			}
		}
		return nil
	}
	var out [][]string
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			out = append(out, objectsWith(child, append(slices.Clip(loc), k), within, depth, name)...)
		}
	case []any:
		for i, child := range node {
			out = append(out, objectsWith(child, append(slices.Clip(loc), strconv.Itoa(i)), within, depth, name)...)
		}
	}
	return out
}

// alternatives reports a failed oneOf or anyOf. When exactly one alternative passed its
// discriminator (no const or enum failure on a direct property), that alternative's
// violations are reported; otherwise one entry on the value itself.
func (f *flattener) alternatives(e *jsonschema.ValidationError, keyword string) {
	var match *jsonschema.ValidationError
	for _, branch := range e.Causes {
		if failsDiscriminator(branch, len(e.InstanceLocation)+1) {
			continue
		}
		if match != nil {
			match = nil // more than one candidate: no discriminator to go by
			break
		}
		match = branch
	}
	if match != nil && len(e.Causes) > 1 {
		f.flatten(match, e.InstanceLocation)
		return
	}
	f.add(f.s.leaf(e, "matches none of the alternatives", keyword))
}

// failsDiscriminator reports whether e has a const or enum failure at instance depth.
func failsDiscriminator(e *jsonschema.ValidationError, depth int) bool {
	switch e.ErrorKind.(type) {
	case *kind.Const, *kind.Enum:
		if len(e.InstanceLocation) == depth {
			return true
		}
	}
	return slices.ContainsFunc(e.Causes, func(c *jsonschema.ValidationError) bool {
		return failsDiscriminator(c, depth)
	})
}

// leaf is the entry for a violation at e's instance location: the value's name, what is
// wrong, the keyword and, when the schema describes the value, the expected form.
func (s *Schema) leaf(e *jsonschema.ValidationError, what, keyword string) Feedback {
	name := "the result"
	if n := len(e.InstanceLocation); n > 0 {
		name = label(e.InstanceLocation[n-1])
	}
	msg := fmt.Sprintf("%s: %s (keyword %s)", name, what, keyword)
	if d, ok := s.subschema(e.SchemaURL)["description"].(string); ok && d != "" {
		msg += "; expected: " + d
	}
	return schemaFeedback(pointer(e.InstanceLocation), msg)
}

// subschema returns the decoded schema object at an absolute schema location inside the
// root resource, or nil.
func (s *Schema) subschema(location string) map[string]any {
	base, frag, _ := strings.Cut(location, "#")
	if base != s.base {
		return nil // inside an embedded resource with its own $id: no description
	}
	frag, err := url.PathUnescape(frag)
	if err != nil || (frag != "" && !strings.HasPrefix(frag, "/")) {
		return nil
	}
	v := s.doc
	if frag != "" {
		for _, tok := range strings.Split(frag[1:], "/") {
			tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
			switch node := v.(type) {
			case map[string]any:
				v = node[tok]
			case []any:
				i, err := strconv.Atoi(tok)
				if err != nil || i < 0 || i >= len(node) {
					return nil
				}
				v = node[i]
			default:
				return nil
			}
		}
	}
	obj, _ := v.(map[string]any)
	return obj
}

// subschemaKeys returns the sorted keys of an object keyword of the schema at location.
func (s *Schema) subschemaKeys(location, keyword string) []string {
	obj, _ := s.subschema(location)[keyword].(map[string]any)
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// enumValues returns the string values of the enum of the schema at location.
func (s *Schema) enumValues(location string) []string {
	values, _ := s.subschema(location)["enum"].([]any)
	var out []string
	for _, v := range values {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func schemaFeedback(ptr, msg string) Feedback {
	return Feedback{Pointer: ptr, Message: msg, Source: SourceSchema}
}

// pointer renders instance location tokens as an RFC 6901 JSON pointer.
func pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

func label(name string) string { return "`" + name + "`" }

func quote(name string) string { return "'" + name + "'" }

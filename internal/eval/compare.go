package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Comparison modes of a compare.yaml rule.
const (
	ModeExact      = "exact"      // equal JSON values (numbers by value)
	ModeNormalized = "normalized" // strings equal after Normalize; other values exactly
	ModeNumeric    = "numeric"    // numbers or numeric strings within Tolerance; others exactly
	ModeSet        = "set"        // arrays equal as multisets; others exactly
	ModeIgnore     = "ignore"     // not compared and not counted
)

// maxMismatches caps the pointers listed per case.
const maxMismatches = 50

// Rule says how the value at Pointer is compared. Pointer is an RFC 6901 JSON pointer
// into expected.json in which a segment "*" matches any array index or object key. The
// value at a matched pointer is compared as one field; ignore drops its whole subtree.
type Rule struct {
	Pointer   string  `yaml:"pointer"`
	Mode      string  `yaml:"mode"`
	Tolerance float64 `yaml:"tolerance"` // numeric: the largest accepted absolute difference
	Elements  string  `yaml:"elements"`  // set: exact (default) or normalized element comparison

	segments []string
	literals int
}

// Rules is a parsed compare.yaml. Values no rule covers are compared leaf by leaf
// (objects by key, arrays by index) with Default.
type Rules struct {
	Default string `yaml:"default"` // exact (the default) or normalized
	Rules   []Rule `yaml:"rules"`
}

// ParseRules decodes and checks a compare.yaml. An empty document is the default rules:
// every value of expected.json compared exactly.
func ParseRules(data []byte) (Rules, error) {
	var r Rules
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return Rules{}, fmt.Errorf("compare rules: %w", err)
	}
	switch r.Default {
	case "":
		r.Default = ModeExact
	case ModeExact, ModeNormalized:
	default:
		return Rules{}, fmt.Errorf("compare rules: default %q: must be exact or normalized", r.Default)
	}
	for i := range r.Rules {
		rule := &r.Rules[i]
		if err := rule.check(); err != nil {
			return Rules{}, fmt.Errorf("compare rules: rules[%d] (%s): %w", i, rule.Pointer, err)
		}
	}
	return r, nil
}

func (r *Rule) check() error {
	if r.Pointer != "" && !strings.HasPrefix(r.Pointer, "/") {
		return errors.New(`pointer must be "" or start with "/"`)
	}
	r.segments = splitPointer(r.Pointer)
	r.literals = 0
	for _, s := range r.segments {
		if s != "*" {
			r.literals++
		}
	}
	switch r.Mode {
	case ModeExact, ModeNormalized, ModeIgnore:
	case ModeNumeric:
		if r.Tolerance < 0 || math.IsNaN(r.Tolerance) {
			return errors.New("tolerance must not be negative")
		}
	case ModeSet:
		if r.Elements != "" && r.Elements != ModeExact && r.Elements != ModeNormalized {
			return fmt.Errorf("elements %q: must be exact or normalized", r.Elements)
		}
	default:
		return fmt.Errorf("mode %q: must be exact, normalized, numeric, set or ignore", r.Mode)
	}
	if r.Tolerance != 0 && r.Mode != ModeNumeric {
		return errors.New("tolerance is only for mode numeric")
	}
	if r.Elements != "" && r.Mode != ModeSet {
		return errors.New("elements is only for mode set")
	}
	return nil
}

func splitPointer(p string) []string {
	if p == "" {
		return nil
	}
	parts := strings.Split(p[1:], "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}

func joinPointer(segments []string) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// match returns the most specific rule matching the pointer segments: the one with the
// most literal segments, the later one in the file on a tie. Nil when none matches.
func (r Rules) match(segments []string) *Rule {
	var best *Rule
	for i := range r.Rules {
		rule := &r.Rules[i]
		if len(rule.segments) != len(segments) {
			continue
		}
		ok := true
		for j, s := range rule.segments {
			if s != "*" && s != segments[j] {
				ok = false
				break
			}
		}
		if ok && (best == nil || rule.literals >= best.literals) {
			best = rule
		}
	}
	return best
}

// Fields is the outcome of comparing one result with its ground truth.
type Fields struct {
	Total   int `json:"total"`   // compared fields
	Matched int `json:"matched"` // compared fields equal under their rule
	// Mismatches are the pointers of the fields that differ, at most 50 (never values).
	Mismatches []string `json:"mismatches,omitempty"`
	// NoResult is set when there was no result to compare: nothing matched.
	NoResult bool `json:"no_result,omitempty"`
}

// Correct reports whether there was a result and every compared field matched.
func (f Fields) Correct() bool { return !f.NoResult && f.Matched == f.Total }

// Accuracy is Matched/Total: 0 without a result, 1 when nothing is compared.
func (f Fields) Accuracy() float64 {
	switch {
	case f.NoResult:
		return 0
	case f.Total == 0:
		return 1
	}
	return float64(f.Matched) / float64(f.Total)
}

// Compare compares a result with the expected JSON under r. A nil or JSON null result
// (no candidate at all) matches nothing. An absent key in the result counts as null. Keys
// the result has and expected.json lacks are not compared: the schema governs shape.
func (r Rules) Compare(expected, result json.RawMessage) (Fields, error) {
	var exp, act any
	if err := json.Unmarshal(expected, &exp); err != nil {
		return Fields{}, fmt.Errorf("expected: %w", err)
	}
	none := len(bytes.TrimSpace(result)) == 0 || bytes.Equal(bytes.TrimSpace(result), []byte("null"))
	if !none {
		if err := json.Unmarshal(result, &act); err != nil {
			none = true // not JSON: as if there were no result
		}
	}
	var f Fields
	r.walk(&f, nil, exp, act)
	if none {
		f = Fields{Total: f.Total, NoResult: true}
	}
	return f, nil
}

func (r Rules) walk(f *Fields, segments []string, exp, act any) {
	if rule := r.match(segments); rule != nil {
		if rule.Mode != ModeIgnore {
			f.add(segments, rule.equal(exp, act))
		}
		return
	}
	switch e := exp.(type) {
	case map[string]any:
		if len(e) > 0 {
			a, _ := act.(map[string]any)
			for _, k := range slices.Sorted(maps.Keys(e)) {
				r.walk(f, append(slices.Clip(segments), k), e[k], a[k])
			}
			return
		}
	case []any:
		if len(e) > 0 {
			a, _ := act.([]any)
			for i, v := range e {
				var av any
				if i < len(a) {
					av = a[i]
				}
				r.walk(f, append(slices.Clip(segments), strconv.Itoa(i)), v, av)
			}
			for i := len(e); i < len(a); i++ {
				f.add(append(slices.Clip(segments), strconv.Itoa(i)), false) // an element too many
			}
			return
		}
	}
	f.add(segments, equalMode(r.Default, exp, act))
}

func (f *Fields) add(segments []string, matched bool) {
	f.Total++
	if matched {
		f.Matched++
	} else if len(f.Mismatches) < maxMismatches {
		f.Mismatches = append(f.Mismatches, joinPointer(segments))
	}
}

func (r *Rule) equal(exp, act any) bool {
	switch r.Mode {
	case ModeNumeric:
		x, okx := number(exp)
		y, oky := number(act)
		if okx && oky {
			return math.Abs(x-y) <= r.Tolerance+1e-12
		}
	case ModeSet:
		x, okx := exp.([]any)
		y, oky := act.([]any)
		if okx && oky {
			return sameMultiset(x, y, r.Elements == ModeNormalized)
		}
	}
	return equalMode(r.Mode, exp, act)
}

func equalMode(mode string, exp, act any) bool {
	if mode == ModeNormalized {
		if x, ok := exp.(string); ok {
			y, ok := act.(string)
			return ok && Normalize(x) == Normalize(y)
		}
	}
	return reflect.DeepEqual(exp, act)
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		x, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return x, err == nil
	}
	return 0, false
}

func sameMultiset(x, y []any, normalized bool) bool {
	if len(x) != len(y) {
		return false
	}
	count := make(map[string]int, len(x))
	for _, v := range x {
		count[canonical(v, normalized)]++
	}
	for _, v := range y {
		k := canonical(v, normalized)
		if count[k] == 0 {
			return false
		}
		count[k]--
	}
	return true
}

// canonical is the JSON encoding of v (object keys sorted), its strings normalized when
// asked.
func canonical(v any, normalized bool) string {
	if normalized {
		v = normalizeStrings(v)
	}
	b, _ := json.Marshal(v) // maps marshal with sorted keys; decoded JSON always encodes
	return string(b)
}

func normalizeStrings(v any) any {
	switch t := v.(type) {
	case string:
		return Normalize(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeStrings(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalizeStrings(e)
		}
		return out
	}
	return v
}

var typography = strings.NewReplacer(
	"‘", "'", "’", "'", "“", `"`, "”", `"`, "„", `"`,
	"–", "-", "—", "-", " ", " ",
)

// Normalize is the normalized-string form: typographic quotes, dashes and non-breaking
// spaces replaced by their ASCII forms, lower case, whitespace runs collapsed to one
// space, surrounding whitespace and one trailing full stop removed.
func Normalize(s string) string {
	s = strings.Join(strings.Fields(strings.ToLower(typography.Replace(s))), " ")
	return strings.TrimSpace(strings.TrimSuffix(s, "."))
}

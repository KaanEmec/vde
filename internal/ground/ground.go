package ground

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Source is the feedback source of grounding entries.
const Source = "grounding"

// Message is the fixed feedback message of a miss. It never quotes the value, so it is
// safe wherever feedback goes.
const Message = "value not found in the document"

// DefaultMinLength is the default Options.MinLength.
const DefaultMinLength = 2

// Options tune Check. The zero value is the default.
type Options struct {
	// MinLength skips strings with fewer runes (after trimming); 0 means
	// DefaultMinLength.
	MinLength int
	// MaxMisses caps Result.Ungrounded; 0 means no cap. Counts are never capped.
	MaxMisses int
}

// Kind is the kind of value a leaf held. It is what a Miss says about the value, which
// it never carries.
type Kind uint8

// Kinds.
const (
	KindString Kind = iota
	KindNumber
	KindDate
	KindDateTime
	KindTime
)

var kindNames = [...]string{"string", "number", "date", "datetime", "time"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// Miss is one ungrounded leaf: its RFC 6901 pointer, the kind of value it held and
// whether it is critical. It never carries the value.
type Miss struct {
	Pointer  string
	Kind     Kind
	Critical bool
}

// Misses are the ungrounded leaves of a candidate, in document order.
type Misses []Miss

// Feedback is a grounding feedback entry. Its fields and tags match extract.Feedback,
// so the loop converts with extract.Feedback(f).
type Feedback struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

// Feedback returns one entry per miss with Source and the fixed Message.
func (m Misses) Feedback() []Feedback {
	if len(m) == 0 {
		return nil
	}
	out := make([]Feedback, len(m))
	for i, x := range m {
		out[i] = Feedback{Pointer: x.Pointer, Message: Message, Source: Source}
	}
	return out
}

// Result is the outcome of Check.
type Result struct {
	Ungrounded     Misses // capped by Options.MaxMisses
	Checked        int    // leaves compared against the text
	Skipped        int    // booleans, nulls, enum values, short strings, derived and skip leaves
	Missed         int    // checked leaves not grounded, uncapped
	CriticalMissed int    // critical leaves not grounded, uncapped
	// Ungroundable is set when there is no text to check against (nil *Text) or the
	// candidate is not valid JSON; nothing was checked.
	Ungroundable bool
}

// CriticalOK reports whether the candidate could be checked and every critical leaf is
// grounded: the grounding condition of the auto judge policy.
func (r Result) CriticalOK() bool { return !r.Ungroundable && r.CriticalMissed == 0 }

// Check walks every leaf of candidate in document order and reports the checked leaves
// that doc does not ground. rules may be nil (schema-less: every leaf checked and
// critical). When no visited leaf is annotated critical, every checked leaf is critical.
// A nil doc gives an Ungroundable result with no misses.
func Check(doc *Text, candidate json.RawMessage, rules Rules, opts Options) Result {
	if doc == nil {
		return Result{Ungroundable: true}
	}
	ls, err := leaves(candidate)
	if err != nil {
		return Result{Ungroundable: true}
	}
	minLen := opts.MinLength
	if minLen <= 0 {
		minLen = DefaultMinLength
	}
	ruleOf := make([]Rule, len(ls))
	enumOf := make([][]string, len(ls))
	anyCritical := false
	if rules != nil {
		for i, l := range ls {
			ruleOf[i], enumOf[i] = rules(l.loc)
			anyCritical = anyCritical || ruleOf[i] == RuleCritical
		}
	}
	var res Result
	for i, l := range ls {
		kind, ok, checked := checkLeaf(doc, l.tok, ruleOf[i], enumOf[i], minLen)
		if !checked {
			res.Skipped++
			continue
		}
		res.Checked++
		if ok {
			continue
		}
		res.Missed++
		critical := !anyCritical || ruleOf[i] == RuleCritical
		if critical {
			res.CriticalMissed++
		}
		if opts.MaxMisses <= 0 || len(res.Ungrounded) < opts.MaxMisses {
			res.Ungrounded = append(res.Ungrounded, Miss{Pointer: pointer(l.loc), Kind: kind, Critical: critical})
		}
	}
	return res
}

// checkLeaf reports the kind of a leaf, whether doc grounds it and whether it was
// checked at all.
func checkLeaf(doc *Text, tok json.Token, rule Rule, enum []string, minLen int) (k Kind, ok, checked bool) {
	if rule == RuleDerived || rule == RuleSkip {
		return 0, false, false
	}
	switch v := tok.(type) {
	case string:
		s := strings.TrimSpace(v)
		if slices.Contains(enum, v) || utf8.RuneCountInString(s) < minLen || Normalize(s) == "" {
			return 0, false, false
		}
		k, ok := doc.match(s)
		return k, ok, true
	case json.Number:
		f, err := v.Float64()
		if err == nil && enumHasNumber(enum, f) {
			return 0, false, false
		}
		ok := err == nil && doc.ContainsNumber(f) || doc.containsPhrase(Normalize(v.String()))
		return KindNumber, ok, true
	}
	return 0, false, false // booleans and nulls
}

func enumHasNumber(enum []string, f float64) bool {
	for _, e := range enum {
		if g, err := strconv.ParseFloat(e, 64); err == nil && g == f {
			return true
		}
	}
	return false
}

type leaf struct {
	loc []string
	tok json.Token // string, json.Number, bool or nil
}

// maxDepth bounds candidate nesting.
const maxDepth = 1000

var errCandidate = errors.New("ground: candidate is not one JSON value")

// leaves returns the scalar leaves of a JSON value in document order.
func leaves(candidate []byte) ([]leaf, error) {
	dec := json.NewDecoder(bytes.NewReader(candidate))
	dec.UseNumber()
	var out []leaf
	var walk func(loc []string) error
	walk = func(loc []string) error {
		if len(loc) > maxDepth {
			return errCandidate
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		loc = slices.Clip(loc)
		switch tok {
		case json.Delim('{'):
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := k.(string)
				if err := walk(append(loc, key)); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if err := walk(append(loc, strconv.Itoa(i))); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		out = append(out, leaf{loc: loc, tok: tok})
		return nil
	}
	if err := walk(nil); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errCandidate
	}
	return out, nil
}

// pointer is the RFC 6901 pointer of a location.
func pointer(loc []string) string {
	var b strings.Builder
	for _, s := range loc {
		b.WriteByte('/')
		b.WriteString(escapePointer(s))
	}
	return b.String()
}

func escapePointer(s string) string {
	if !strings.ContainsAny(s, "~/") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

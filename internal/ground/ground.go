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

// minLength: strings with fewer runes (after trimming) are skipped.
const minLength = 2

// Miss is one ungrounded leaf: its RFC 6901 pointer and whether it is critical. It
// never carries the value.
type Miss struct {
	Pointer  string
	Critical bool
}

// Result is the outcome of Check.
type Result struct {
	Ungrounded []Miss // in document order, capped by Check's maxMisses
	// CriticalChecked counts the critical leaves compared against the text: a candidate
	// with none (an empty object, only nulls, booleans or enum values) gives grounding no
	// evidence.
	CriticalChecked int
	CriticalMissed  int // critical leaves not grounded, uncapped
	// Ungroundable is set when there is no text to check against (nil *Text) or the
	// candidate is not valid JSON; nothing was checked.
	Ungroundable bool
}

// CriticalOK reports whether the candidate could be checked, at least one critical leaf
// was checked and every critical leaf is grounded: the grounding condition of the auto
// judge policy. A candidate with nothing to check never passes it.
func (r Result) CriticalOK() bool {
	return !r.Ungroundable && r.CriticalChecked > 0 && r.CriticalMissed == 0
}

// Check walks every leaf of candidate in document order and reports the checked leaves
// that doc does not ground. rules may be nil (schema-less: every leaf checked and
// critical). When no visited leaf is annotated critical, every checked leaf is critical.
// Result.Ungrounded holds at most maxMisses entries (0 means no cap); counts are never
// capped. A nil doc gives an Ungroundable result with no misses.
func Check(doc *Text, candidate json.RawMessage, rules Rules, maxMisses int) Result {
	if doc == nil {
		return Result{Ungroundable: true}
	}
	ls, err := leaves(candidate)
	if err != nil {
		return Result{Ungroundable: true}
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
		ok, checked := checkLeaf(doc, l.tok, ruleOf[i], enumOf[i])
		if !checked {
			continue
		}
		critical := !anyCritical || ruleOf[i] == RuleCritical
		if critical {
			res.CriticalChecked++
		}
		if ok {
			continue
		}
		if critical {
			res.CriticalMissed++
		}
		if maxMisses <= 0 || len(res.Ungrounded) < maxMisses {
			res.Ungrounded = append(res.Ungrounded, Miss{Pointer: Pointer(l.loc), Critical: critical})
		}
	}
	return res
}

// checkLeaf reports whether doc grounds a leaf and whether it was checked at all
// (booleans, nulls, enum values, short strings, derived and skip leaves are not).
func checkLeaf(doc *Text, tok json.Token, rule Rule, enum []string) (ok, checked bool) {
	if rule == RuleDerived || rule == RuleSkip {
		return false, false
	}
	switch v := tok.(type) {
	case string:
		s := strings.TrimSpace(v)
		if slices.Contains(enum, v) || utf8.RuneCountInString(s) < minLength || Normalize(s) == "" {
			return false, false
		}
		return doc.match(s), true
	case json.Number:
		f, err := v.Float64()
		if err == nil && enumHasNumber(enum, f) {
			return false, false
		}
		return err == nil && doc.ContainsNumber(f) || doc.containsPhrase(Normalize(v.String())), true
	}
	return false, false // booleans and nulls
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

// Pointer renders location tokens as an RFC 6901 JSON pointer, "" for none. It, with
// SplitPointer, UnescapeToken and ValidPointer, is the service's one JSON pointer
// implementation.
func Pointer(tokens []string) string {
	var b strings.Builder
	for _, s := range tokens {
		b.WriteByte('/')
		b.WriteString(escapePointer(s))
	}
	return b.String()
}

// SplitPointer returns the unescaped tokens of the pointer p, nil for "".
func SplitPointer(p string) []string {
	if p == "" {
		return nil
	}
	tokens := strings.Split(p[1:], "/")
	for i, t := range tokens {
		tokens[i] = UnescapeToken(t)
	}
	return tokens
}

// UnescapeToken unescapes one pointer token: ~1 is /, ~0 is ~.
func UnescapeToken(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
}

// ValidPointer reports whether p is an RFC 6901 JSON pointer.
func ValidPointer(p string) bool {
	if p == "" {
		return true
	}
	if p[0] != '/' {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' && (i+1 == len(p) || (p[i+1] != '0' && p[i+1] != '1')) {
			return false
		}
	}
	return true
}

func escapePointer(s string) string {
	if !strings.ContainsAny(s, "~/") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

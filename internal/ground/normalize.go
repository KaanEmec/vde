package ground

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Normalize returns the comparison form of s: NFKD, combining marks (Mn) and format
// characters (Cf, such as soft hyphens) dropped, lower-cased with Turkish dotted and
// dotless i folded to i and ß to ss, whitespace and control characters collapsed to one
// space, and every rune that is neither a letter, a digit nor a mark stripped at the
// edges of each space-separated token. Punctuation inside a token is kept, so LX42QZ,
// A-12, 10:45 and 21.03.2027 survive. Invalid UTF-8 becomes U+FFFD. Normalize is
// idempotent.
func Normalize(s string) string {
	d := norm.NFKD.String(strings.ToValidUTF8(s, "\uFFFD"))
	var b strings.Builder
	b.Grow(len(d))
	tok := make([]rune, 0, 64)
	flush := func() {
		start, end := 0, len(tok)
		for start < end && !isWord(tok[start]) {
			start++
		}
		for end > start && !isWord(tok[end-1]) {
			end--
		}
		if start < end {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			for _, r := range tok[start:end] {
				b.WriteRune(r)
			}
		}
		tok = tok[:0]
	}
	for _, r := range d {
		if r < utf8.RuneSelf {
			switch {
			case r >= 'A' && r <= 'Z':
				tok = append(tok, r+('a'-'A'))
			case r <= ' ' || r == 0x7f:
				flush()
			default:
				tok = append(tok, r)
			}
			continue
		}
		switch {
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Cf, r):
			// dropped: diacritics after NFKD, soft hyphens, zero-width spaces
		case unicode.IsSpace(r), unicode.IsControl(r):
			flush()
		case r == 'ı': // dotless i; dotted İ is I plus a combining dot after NFKD
			tok = append(tok, 'i')
		case r == 'ß', r == 'ẞ':
			tok = append(tok, 's', 's')
		default:
			tok = append(tok, unicode.ToLower(r))
		}
	}
	flush()
	return b.String()
}

// isWord reports whether r is part of a word: a letter, a digit or a mark.
func isWord(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z'
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// Text is a document's text prepared for grounding: the normalized text, the positions
// of its words (maximal runs of letters, digits and marks) and the numbers it prints.
// Build it once per document with NewText; it is read-only and safe for concurrent use.
type Text struct {
	norm  string
	words map[string][]int // word -> ascending byte offsets in norm
	nums  []float64        // ascending, absolute values of every printed number reading
}

// NewText normalizes s and indexes it.
func NewText(s string) *Text {
	t := &Text{norm: Normalize(s), words: make(map[string][]int)}
	start := -1
	for i, r := range t.norm {
		switch {
		case isWord(r):
			if start < 0 {
				start = i
			}
		case start >= 0:
			w := t.norm[start:i]
			t.words[w] = append(t.words[w], start)
			start = -1
		}
	}
	if start >= 0 {
		w := t.norm[start:]
		t.words[w] = append(t.words[w], start)
	}
	t.nums = scanNumbers(t.norm)
	return t
}

// Contains reports whether the string value is grounded in the text: as an ISO date,
// datetime or time (dates.go), as a phrase on word boundaries, as reordered words within
// a short window, or as a number (numbers.go).
func (t *Text) Contains(value string) bool {
	_, ok := t.match(value)
	return ok
}

// match classifies value and reports whether it is grounded.
func (t *Text) match(value string) (Kind, bool) {
	if k, ok, isTemporal := t.matchTemporal(strings.TrimSpace(value)); isTemporal {
		return k, ok
	}
	c := Normalize(value)
	if c == "" {
		return KindString, true
	}
	if t.containsPhrase(c) || t.containsReordered(c) {
		return KindString, true
	}
	if readings, ok := parseCandidateNumber(value); ok {
		for _, v := range readings {
			if t.ContainsNumber(v) {
				return KindString, true
			}
		}
	}
	return KindString, false
}

// containsPhrase reports whether the normalized phrase c occurs with no letter, digit or
// mark directly before or after it.
func (t *Text) containsPhrase(c string) bool {
	if isSingleWord(c) {
		return len(t.words[c]) > 0
	}
	return t.indexBounded(c, func(prev, first rune) bool { return !isWord(prev) })
}

// containsTemporal reports whether the normalized date or time form c occurs with no
// digit next to a digit at either end and no word rune next to a letter. A letter next to
// a digit is allowed, so 2027-03-21t14:45 prints both 2027-03-21 and 14:45.
func (t *Text) containsTemporal(c string) bool {
	return t.indexBounded(c, func(outer, inner rune) bool {
		switch {
		case isDigit(inner):
			return !isDigit(outer)
		case isWord(inner):
			return !isWord(outer)
		}
		return true
	})
}

// indexBounded finds c in the normalized text where ok accepts both edges; ok gets the
// rune outside the match (utf8.RuneError at the text's ends, which is no word rune) and
// the rune at that edge of the match.
func (t *Text) indexBounded(c string, ok func(outer, inner rune) bool) bool {
	first, _ := utf8.DecodeRuneInString(c)
	last, _ := utf8.DecodeLastRuneInString(c)
	for from := 0; from < len(t.norm); {
		i := strings.Index(t.norm[from:], c)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(c)
		prev, next := utf8.RuneError, utf8.RuneError
		if start > 0 {
			prev, _ = utf8.DecodeLastRuneInString(t.norm[:start])
		}
		if end < len(t.norm) {
			next, _ = utf8.DecodeRuneInString(t.norm[end:])
		}
		if ok(prev, first) && ok(next, last) {
			return true
		}
		_, size := utf8.DecodeRuneInString(t.norm[start:])
		from = start + size
	}
	return false
}

// reorderMinWords and reorderMaxWords bound the phrases that may match reordered, and
// reorderSlack is how far, beyond the phrase's own length, its words may spread.
const (
	reorderMinWords = 2
	reorderMaxWords = 6
	reorderSlack    = 16
)

// containsReordered reports whether every word of the normalized phrase c occurs within
// len(c)+reorderSlack bytes of one occurrence of its rarest word, in any order. It grounds
// names printed surname first (SMITH/JOHN MR for "John Smith").
func (t *Text) containsReordered(c string) bool {
	ws := splitWords(c)
	if len(ws) < reorderMinWords || len(ws) > reorderMaxWords {
		return false
	}
	rarest := -1
	for i, w := range ws {
		pos := t.words[w]
		if len(pos) == 0 {
			return false
		}
		if rarest < 0 || len(pos) < len(t.words[ws[rarest]]) {
			rarest = i
		}
	}
	window := len(c) + reorderSlack
	for _, p := range t.words[ws[rarest]] {
		all := true
		for _, w := range ws {
			pos := t.words[w]
			j := sort.SearchInts(pos, p-window)
			if j == len(pos) || pos[j] > p+window {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func isSingleWord(s string) bool {
	for _, r := range s {
		if !isWord(r) {
			return false
		}
	}
	return s != ""
}

// splitWords splits s into its words, dropping every rune that is not a word rune.
func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isWord(r) })
}

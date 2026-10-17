package ground

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// numberTolerance is how far a printed number may be from a candidate number and still
// ground it (half a cent: 33.333 is grounded by a printed 33.33).
const numberTolerance = 0.005

// ContainsNumber reports whether the text prints a number within 0.005 of v. Signs are
// ignored on both sides.
func (t *Text) ContainsNumber(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	v = math.Abs(v)
	const eps = numberTolerance + 1e-9
	i, _ := slices.BinarySearch(t.nums, v-eps)
	return i < len(t.nums) && t.nums[i] <= v+eps
}

// scanNumbers returns every reading of every number printed in the normalized text s,
// sorted and without duplicates. A digit run directly after a letter or digit (LX42) is
// not a number.
func scanNumbers(s string) []float64 {
	var out []float64
	for i := 0; i < len(s); {
		if !isDigit(rune(s[i])) {
			i++
			continue
		}
		groups, seps, end := scanRun(s, i)
		if prev, _ := utf8.DecodeLastRuneInString(s[:i]); i == 0 || !isWord(prev) {
			out = appendReadings(out, groups, seps)
		}
		i = end
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// scanRun reads the number starting at s[i]: digit groups separated by '.' or ',' before
// a digit, or by a space before exactly three digits (a thousands group). It returns the
// groups, the separators between them and the end offset.
func scanRun(s string, i int) (groups []string, seps []byte, end int) {
	j := i
	for {
		k := j
		for k < len(s) && isDigit(rune(s[k])) {
			k++
		}
		groups = append(groups, s[j:k])
		switch {
		case k+1 < len(s) && (s[k] == '.' || s[k] == ',') && isDigit(rune(s[k+1])):
		case k+4 <= len(s) && s[k] == ' ' && allDigits(s[k+1:k+4]) &&
			(k+4 == len(s) || !isDigit(rune(s[k+4]))):
		default:
			return groups, seps, k
		}
		seps = append(seps, s[k])
		j = k + 1
	}
}

// appendReadings appends the values of a scanned run. A run with no valid reading
// (21.03.2027) yields each group as an integer. A run joined by spaces also yields the
// readings of its space-separated parts, since 2 100 may be two numbers.
func appendReadings(out []float64, groups []string, seps []byte) []float64 {
	readings := parseGroups(groups, seps)
	for _, r := range readings {
		if v, err := strconv.ParseFloat(r, 64); err == nil {
			out = append(out, v)
		}
	}
	if sp := slices.Index(seps, ' '); sp >= 0 {
		out = appendReadings(out, groups[:sp+1], seps[:sp])
		return appendReadings(out, groups[sp+1:], seps[sp+1:])
	}
	if len(readings) == 0 {
		for _, g := range groups {
			if v, err := strconv.ParseFloat(g, 64); err == nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// parseGroups returns the canonical decimal strings a run can mean: every separator a
// thousands separator, or the last one ('.' or ',') a decimal point after thousands
// separators of another kind. 1.234 means both 1234 and 1.234.
func parseGroups(groups []string, seps []byte) []string {
	n := len(groups)
	if n == 1 {
		return groups
	}
	var out []string
	if thousands(groups, seps) {
		out = append(out, strings.Join(groups, ""))
	}
	last := seps[n-2]
	if last != ' ' && thousands(groups[:n-1], seps[:n-2]) && !slices.Contains(seps[:n-2], last) {
		out = append(out, strings.Join(groups[:n-1], "")+"."+groups[n-1])
	}
	return out
}

// thousands reports whether the groups form an integer with thousands separators: one
// to three leading digits, three digits per later group, one separator kind throughout.
func thousands(groups []string, seps []byte) bool {
	if len(groups) == 1 {
		return true
	}
	if len(groups[0]) > 3 {
		return false
	}
	for i, g := range groups[1:] {
		if len(g) != 3 || seps[i] != seps[0] {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(rune(s[i])) {
			return false
		}
	}
	return s != ""
}

// parseCandidateNumber reads a string candidate as a number: an optional sign, currency
// symbols and three-letter currency codes around it are ignored, and the rest must be one
// run in either decimal convention. It returns every reading (1.234 gives two).
func parseCandidateNumber(s string) ([]float64, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimFunc(s, func(r rune) bool {
		return r == ' ' || r == '+' || r == '-' || unicode.Is(unicode.Sc, r)
	})
	s = trimCurrencyCode(s)
	if s == "" || !isDigit(rune(s[0])) {
		return nil, false
	}
	groups, seps, end := scanRun(s, 0)
	if end != len(s) {
		return nil, false
	}
	var out []float64
	for _, r := range parseGroups(groups, seps) {
		if v, err := strconv.ParseFloat(r, 64); err == nil {
			out = append(out, v)
		}
	}
	return out, len(out) > 0
}

// trimCurrencyCode removes one leading or trailing three-letter code (EUR 12, 12 TRY) and
// the sign and currency symbols left beside the number.
func trimCurrencyCode(s string) string {
	isCode := func(c string) bool {
		for i := 0; i < len(c); i++ {
			if b := c[i] | 0x20; b < 'a' || b > 'z' {
				return false
			}
		}
		return true
	}
	if len(s) > 3 && isCode(s[:3]) && !isLetterByte(s[3]) {
		s = s[3:]
	} else if len(s) > 3 && isCode(s[len(s)-3:]) && !isLetterByte(s[len(s)-4]) {
		s = s[:len(s)-3]
	}
	return strings.TrimFunc(s, func(r rune) bool {
		return r == ' ' || r == '+' || r == '-' || unicode.Is(unicode.Sc, r)
	})
}

func isLetterByte(b byte) bool { return b >= utf8.RuneSelf || b|0x20 >= 'a' && b|0x20 <= 'z' }

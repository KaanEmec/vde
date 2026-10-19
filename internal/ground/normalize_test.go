package ground

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"ascii case", "Hello WORLD", "hello world"},
		{"diacritics", "Café Müller Ñandú Øre", "cafe muller nandu øre"},
		{"turkish dotted capital I", "İSTANBUL", "istanbul"},
		{"turkish dotless i", "Iğdır ılık", "igdir ilik"},
		{"plain capital I", "ISTANBUL", "istanbul"},
		{"sharp s", "Straße STRAẞE", "strasse strasse"},
		{"ligature and fullwidth", "ﬁle ＡＢ１２", "file ab12"},
		{"whitespace collapsed", "  a \t\n b\u00a0\u2009c\u202f d  ", "a b c d"},
		{"control characters separate", "a\x00b\x7fc", "a b c"},
		{"soft hyphen and zero width dropped", "Reise\u00adbüro Zero\u200bWidth", "reiseburo zerowidth"},
		{"punctuation at edges", "(LX42QZ), «Berlin»! \"x\"", "lx42qz berlin x"},
		{"punctuation inside kept", "A-12 10:45 21.03.2027 a/b", "a-12 10:45 21.03.2027 a/b"},
		{"german date", "21. März 2027", "21 marz 2027"},
		{"us date comma", "March 21, 2027", "march 21 2027"},
		{"symbols at edges", "€1.234,50 $12 100%", "1.234,50 12 100"},
		{"only punctuation", "-- !! ...", ""},
		{"mark-only token", "\u0301", ""},
		{"greek", "ΑΘΗΝΑ", "αθηνα"},
		{"invalid utf8", "a\xffb", "a�b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"", "İSTANBUL", "Straße", "(LX42QZ),", "1.234,50 €", "ﬁ", "a\u00adb", "\u0301x", "Ω K Å", "ǅ ǈ", "a\xffb"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := Normalize(s)
		if !utf8.ValidString(n) {
			t.Fatalf("Normalize(%q) is not valid UTF-8", s)
		}
		if again := Normalize(n); again != n {
			t.Fatalf("not idempotent for %q: %q then %q", s, n, again)
		}
		if strings.HasPrefix(n, " ") || strings.HasSuffix(n, " ") || strings.Contains(n, "  ") {
			t.Fatalf("Normalize(%q) = %q has stray spaces", s, n)
		}
	})
}

func TestText_Contains_strings(t *testing.T) {
	t.Parallel()
	doc := NewText("Passenger: SMITH/JOHN MR\nHotel: Café Müller, İSTANBUL (Beyoğlu)\n" +
		"Booking LX42QZ, seat A-12. Berliner Weiße served.\n" + strings.Repeat("filler ", 20) + "Jones")
	tests := []struct {
		name, value string
		want        bool
	}{
		{"case and diacritics", "cafe muller", true},
		{"turkish casing", "Istanbul", true},
		{"turkish source letters", "Beyoğlu", true},
		{"token with digits", "lx42qz", true},
		{"punctuation inside token", "A-12", true},
		{"edge punctuation ignored", "(A-12).", true},
		{"phrase across lines", "MR Hotel", true},
		{"reordered name", "John Smith", true},
		{"reordered words too far apart", "Jones Smith", false},
		{"reordered with an absent word", "John Smythe", false},
		{"no partial word", "Berlin", false},
		{"no partial token", "LX42", false},
		{"no partial phrase", "Café Müll", false},
		{"invented", "Grand Hotel", false},
		{"punctuation only is trivially present", "--", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := doc.Contains(tt.value); got != tt.want {
				t.Errorf("Contains(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// syntheticText returns about n bytes of invoice-like text with varying values.
func syntheticText(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "Pos %d Leistung Nr. RX%05d Übernachtung Doppelzimmer am %02d.%02d.2027, "+
			"Betrag %d.%03d,%02d EUR (netto), Gast Müller/Şahin İŞLEM\n", i, i, i%28+1, i%12+1, i%9, i%1000, i%100)
	}
	return b.String()
}

func TestNewText_performance(t *testing.T) {
	text := syntheticText(200 << 10)
	candidate := []byte(`{"a":"Übernachtung Doppelzimmer","b":"2027-01-01","c":1234.34,"d":"Grand Hotel","e":"2027-12-31T23:59","f":"RX00042"}`)
	// A loose bound: it catches a quadratic regression (seconds), not scheduler noise.
	// Under -race with coverage on a shared CI runner the same work has taken 1.3 s.
	limit := 500 * time.Millisecond
	if raceEnabled {
		limit *= 10
	}
	start := time.Now()
	doc := NewText(text)
	res := Check(doc, candidate, nil, Options{})
	if d := time.Since(start); d > limit {
		t.Errorf("NewText and Check on %d bytes took %v, want under %v", len(text), d, limit)
	}
	if res.Checked != 6 || res.Missed != 2 {
		t.Errorf("Checked, Missed = %d, %d, want 6, 2", res.Checked, res.Missed)
	}
}

func BenchmarkNormalize(b *testing.B) {
	text := syntheticText(200 << 10)
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		Normalize(text)
	}
}

func BenchmarkNewText(b *testing.B) {
	text := syntheticText(200 << 10)
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		NewText(text)
	}
}

// Contains reports whether the string value is grounded in the text: as an ISO date,
// datetime or time (dates.go), as a phrase on word boundaries, as reordered words within
// a short window, or as a number (numbers.go).
func (t *Text) Contains(value string) bool {
	_, ok := t.match(value)
	return ok
}

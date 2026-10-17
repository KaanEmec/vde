package ground

import (
	"math"
	"testing"
)

func TestText_ContainsNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, doc string
		value     float64
		want      bool
	}{
		{"comma decimal, dot thousands", "Total 1.234,50 EUR", 1234.5, true},
		{"dot decimal, comma thousands", "Total 1,234.50", 1234.5, true},
		{"space thousands", "Total: 1 234,50 €", 1234.5, true},
		{"thin space thousands", "1 234,50", 1234.5, true},
		{"no-break space thousands", "1 234.50", 1234.5, true},
		{"several thousands groups", "1.234.567,89", 1234567.89, true},
		{"space groups", "12 345 678", 12345678, true},
		{"space-joined parts also count alone", "2 100", 2, true},
		{"ambiguous dot as thousands", "1.234", 1234, true},
		{"ambiguous dot as decimal", "1.234", 1.234, true},
		{"ambiguous comma as thousands", "1,234", 1234, true},
		{"ambiguous comma as decimal", "1,234", 1.234, true},
		{"plain decimal", "12.50", 12.5, true},
		{"comma decimal", "0,5 kg", 0.5, true},
		{"integer", "Room 305", 305, true},
		{"currency symbol", "€1.234,50", 1234.5, true},
		{"percent", "19%", 19, true},
		{"sign ignored in text", "-12,50", 12.5, true},
		{"sign ignored in candidate", "12,50", -12.5, true},
		{"within tolerance", "33.33", 33.333, true},
		{"outside tolerance", "33.33", 33.34, false},
		{"prefix of a longer number", "123", 12, false},
		{"suffix of a longer number", "123", 23, false},
		{"after a letter", "LX42", 42, false},
		{"after a currency code letter", "EUR1234", 1234, false},
		{"date is not a decimal", "21.03.2027", 21.03, false},
		{"date parts count as integers", "21.03.2027", 2027, true},
		{"mixed thousands kinds are not one number", "1.234,567,89", 1234567.89, false},
		{"absent", "nothing here", 1, false},
		{"NaN", "1", math.NaN(), false},
		{"infinity", "1", math.Inf(1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NewText(tt.doc).ContainsNumber(tt.value); got != tt.want {
				t.Errorf("text %q: ContainsNumber(%v) = %v, want %v", tt.doc, tt.value, got, tt.want)
			}
		})
	}
}

func TestParseCandidateNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in string
		want     []float64
	}{
		{"plain", "1234.50", []float64{1234.5}},
		{"code before", "EUR 1,234.50", []float64{1234.5}},
		{"code glued before", "EUR1234,5", []float64{1234.5}},
		{"code after", "1.234,50 TRY", []float64{1234.5}},
		{"symbol", "€1.234,50", []float64{1234.5}},
		{"sign and symbol", "-$12", []float64{12}},
		{"ambiguous", "1.234", []float64{1234, 1.234}},
		{"no-break space thousands", "1 234", []float64{1234}},
		{"word", "abc", nil},
		{"version string", "1.2.3", nil},
		{"trailing text", "12 kg", nil},
		{"code only", "EUR", nil},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCandidateNumber(tt.in)
			if ok != (tt.want != nil) || len(got) != len(tt.want) {
				t.Fatalf("parseCandidateNumber(%q) = %v, %v, want %v", tt.in, got, ok, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseCandidateNumber(%q) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestText_Contains_numericStrings(t *testing.T) {
	t.Parallel()
	doc := NewText("Gesamtbetrag 1.234,50 EUR; Total 2,345.60 USD")
	tests := []struct {
		value string
		want  bool
	}{
		{"1234.50", true},
		{"EUR 1234,5", true},
		{"2345.6", true},
		{"$2,345.60", true},
		{"1234.60", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()
			if got := doc.Contains(tt.value); got != tt.want {
				t.Errorf("Contains(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

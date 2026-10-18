package extract

import (
	"testing"
	"unicode/utf8"
)

// FuzzParseVerdict feeds arbitrary judge answers to the verdict parser: it never panics,
// and what it accepts is an accept, or a reject with feedback, whose pointers are RFC 6901
// pointers, whose messages are within the cap and whose confidence is in [0, 1].
func FuzzParseVerdict(f *testing.F) {
	for _, s := range []string{
		`{"verdict": "accept", "feedback": {}}`,
		`{"verdict": "accept"}`,
		`{"verdict": "reject", "feedback": {"/z": "last key first", "/a": "then this", "/z": "again"}}`,
		`{"feedback": {"": "the title is weak"}, "verdict": "accept", "confidence": 0.9}`,
		`{"verdict": "reject", "feedback": {"/a~1b/0": "escaped"}, "confidence": 2}`,
		`{"verdict": "reject", "feedback": {"/a~2": "x"}}`,
		`{"verdict": "reject", "verdict": "accept", "feedback": {}}`,
		`{"verdict": "accept", "feedback": null, "extra": [1, {"x": "y"}]}`,
		"```json\n{\"verdict\": \"reject\", \"feedback\": {\"/flight\": \"wrong \\u00e9 date\"}}\n```",
		`Here is my verdict: {"verdict": "accept", "feedback": {}} done`,
		`{"verdict": "reject", "feedback": {"/a": "` + string(make([]byte, 300)) + `"}}`,
		`{"verdict": "accept", "confidence": 1e999}`,
		`{"verdict": `,
		`[]`,
	} {
		f.Add(s)
	}
	const maxChars = 40
	f.Fuzz(func(t *testing.T, text string) {
		v, err := parseVerdict(text, maxChars)
		if err != nil {
			return
		}
		if !v.accept && len(v.feedback) == 0 {
			t.Fatal("a reject without feedback was accepted")
		}
		for _, fb := range v.feedback {
			if !validPointer(fb.Pointer) || fb.Source != SourceJudge {
				t.Fatalf("feedback entry with pointer %q source %q", fb.Pointer, fb.Source)
			}
			if n := utf8.RuneCountInString(fb.Message); n > maxChars {
				t.Fatalf("message of %d characters, cap %d", n, maxChars)
			}
		}
		if v.confidence != nil && (*v.confidence < 0 || *v.confidence > 1) {
			t.Fatalf("confidence %v outside [0, 1]", *v.confidence)
		}
	})
}

package extract

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseVerdict(t *testing.T) {
	judge := func(ptr, msg string) Feedback { return Feedback{Pointer: ptr, Message: msg, Source: SourceJudge} }
	half := 0.5
	tests := []struct {
		name           string
		text           string
		accept         bool
		feedback       []Feedback
		confidence     *float64
		ignored        bool
		wantUnparsable bool
	}{
		{name: "accept", text: `{"verdict": "accept", "feedback": {}}`, accept: true, feedback: []Feedback{}},
		{name: "accept without feedback key", text: `{"verdict": "accept"}`, accept: true},
		{
			name:     "reject with two entries in order",
			text:     `{"verdict": "reject", "feedback": {"/z": "last key first", "/a": "then this", "/z": "same pointer again"}}`,
			feedback: []Feedback{judge("/z", "last key first"), judge("/a", "then this"), judge("/z", "same pointer again")},
		},
		{
			name:     "accept with advisory",
			text:     `{"feedback": {"": "the title is weak"}, "verdict": "accept"}`,
			accept:   true,
			feedback: []Feedback{judge("", "the title is weak")},
		},
		{name: "reject without entries", text: `{"verdict": "reject", "feedback": {}}`, wantUnparsable: true},
		{name: "reject without feedback key", text: `{"verdict": "reject"}`, wantUnparsable: true},
		{name: "wrong verdict word", text: `{"verdict": "accepted", "feedback": {}}`, wantUnparsable: true},
		{name: "capitalised verdict word", text: `{"verdict": "Accept", "feedback": {}}`, wantUnparsable: true},
		{name: "missing verdict", text: `{"feedback": {}}`, wantUnparsable: true},
		{name: "verdict not a string", text: `{"verdict": true, "feedback": {}}`, wantUnparsable: true},
		{name: "verdict null", text: `{"verdict": null, "feedback": {}}`, wantUnparsable: true},
		{name: "duplicate verdict", text: `{"verdict": "reject", "verdict": "accept", "feedback": {}}`, wantUnparsable: true},
		{name: "duplicate feedback", text: `{"verdict": "accept", "feedback": {}, "feedback": {}}`, wantUnparsable: true},
		{name: "feedback is a list", text: `{"verdict": "reject", "feedback": ["bad"]}`, wantUnparsable: true},
		{name: "feedback null", text: `{"verdict": "accept", "feedback": null}`, wantUnparsable: true},
		{name: "feedback message not a string", text: `{"verdict": "reject", "feedback": {"/a": 1}}`, wantUnparsable: true},
		{name: "feedback message null", text: `{"verdict": "reject", "feedback": {"/a": null}}`, wantUnparsable: true},
		{name: "pointer without slash", text: `{"verdict": "reject", "feedback": {"a/b": "x"}}`, wantUnparsable: true},
		{name: "pointer with bad escape", text: `{"verdict": "reject", "feedback": {"/a~2": "x"}}`, wantUnparsable: true},
		{name: "pointer ending in tilde", text: `{"verdict": "reject", "feedback": {"/a~": "x"}}`, wantUnparsable: true},
		{
			name:     "escaped pointer kept as given",
			text:     `{"verdict": "reject", "feedback": {"/a~1b/~0c/-": "x"}}`,
			feedback: []Feedback{judge("/a~1b/~0c/-", "x")},
		},
		{
			name:     "fenced verdict",
			text:     "```json\n{\"verdict\": \"reject\", \"feedback\": {\"/a\": \"x\"}}\n```",
			feedback: []Feedback{judge("/a", "x")},
		},
		{
			name:     "extra keys ignored",
			text:     `{"verdict": "accept", "category": {"x": [1]}, "feedback": {}, "note": "n"}`,
			accept:   true,
			feedback: []Feedback{},
		},
		{name: "confidence present", text: `{"verdict": "accept", "feedback": {}, "confidence": 0.5}`, accept: true, feedback: []Feedback{}, confidence: &half},
		{name: "confidence out of range", text: `{"verdict": "accept", "feedback": {}, "confidence": 1.5}`, accept: true, feedback: []Feedback{}, ignored: true},
		{name: "confidence negative", text: `{"verdict": "accept", "feedback": {}, "confidence": -0.1}`, accept: true, feedback: []Feedback{}, ignored: true},
		{name: "confidence wrong type", text: `{"verdict": "reject", "feedback": {"": "x"}, "confidence": "high"}`, feedback: []Feedback{judge("", "x")}, ignored: true},
		{name: "not JSON", text: "I accept this candidate.", wantUnparsable: true},
		{name: "truncated", text: `{"verdict": "accept", "feedback": {`, wantUnparsable: true},
		{name: "array", text: `[{"verdict": "accept"}]`, wantUnparsable: true},
		// Answer blocks (ADR 0012).
		{
			name:     "blocks accept",
			text:     "<analysis>\n/a: \"x\" (page 1) ok\nnone\n</analysis>\n<verdict>\n{\"verdict\": \"accept\", \"feedback\": {}}\n</verdict>",
			accept:   true,
			feedback: []Feedback{},
		},
		{
			name:     "blocks reject",
			text:     "<analysis>/a: \"y\" (page 2) disputed</analysis><verdict>{\"verdict\": \"reject\", \"feedback\": {\"/a\": \"Page 2 says \\\"y\\\".\"}}</verdict>",
			feedback: []Feedback{judge("/a", `Page 2 says "y".`)},
		},
		{
			name:     "accept-like value in the analysis",
			text:     "<analysis>not {\"verdict\": \"accept\", \"feedback\": {}} but</analysis>\n<verdict>{\"verdict\": \"reject\", \"feedback\": {\"/a\": \"x\"}}</verdict>",
			feedback: []Feedback{judge("/a", "x")},
		},
		{name: "verdict block without analysis", text: `<verdict>{"verdict": "accept"}</verdict>`, accept: true},
		{name: "analysis without verdict block", text: "<analysis>all ok</analysis>\n{\"verdict\": \"accept\", \"feedback\": {}}", wantUnparsable: true},
		{name: "analysis with an accept and no verdict", text: "<analysis>{\"verdict\": \"accept\", \"feedback\": {}}</analysis>", wantUnparsable: true},
		{name: "unclosed analysis", text: "<analysis>{\"verdict\": \"accept\", \"feedback\": {}}", wantUnparsable: true},
		{name: "unclosed verdict block", text: "<analysis>x</analysis><verdict>{\"verdict\": \"accept\", \"feedback\": {}}", wantUnparsable: true},
		{name: "two verdict blocks", text: "<verdict>{\"verdict\": \"reject\", \"feedback\": {\"\": \"x\"}}</verdict><verdict>{\"verdict\": \"accept\"}</verdict>", wantUnparsable: true},
		{name: "invalid verdict block", text: "<analysis>x</analysis><verdict>{\"verdict\": \"accept\",}</verdict>", wantUnparsable: true},
		{name: "json block instead of verdict", text: "<analysis>x</analysis><json>{\"verdict\": \"accept\"}</json>", wantUnparsable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := parseVerdict(tt.text, DefaultFeedbackMessageChars)
			if tt.wantUnparsable {
				if err == nil {
					t.Fatalf("parseVerdict(%q) = %+v, want unparseable", tt.text, v)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVerdict(%q): %v", tt.text, err)
			}
			if v.accept != tt.accept || !reflect.DeepEqual(v.feedback, tt.feedback) || v.ignoredConfidence != tt.ignored {
				t.Errorf("parseVerdict(%q) = %+v, want accept=%v feedback=%+v ignored=%v", tt.text, v, tt.accept, tt.feedback, tt.ignored)
			}
			if (v.confidence == nil) != (tt.confidence == nil) || (v.confidence != nil && *v.confidence != *tt.confidence) {
				t.Errorf("confidence = %v, want %v", v.confidence, tt.confidence)
			}
		})
	}
}

func TestParseVerdict_messageCap(t *testing.T) {
	long := strings.Repeat("é", 30)
	v, err := parseVerdict(`{"verdict": "reject", "feedback": {"/a": "`+long+`", "/b": "short"}}`, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.feedback[0].Message; got != strings.Repeat("é", 9)+"…" || utf8.RuneCountInString(got) != 10 {
		t.Errorf("capped message = %q, want 9 characters and an ellipsis", got)
	}
	if got := v.feedback[1].Message; got != "short" {
		t.Errorf("short message = %q, want it unchanged", got)
	}
}

func TestCapMessage(t *testing.T) {
	tests := []struct {
		name, msg string
		max       int
		want      string
	}{
		{"under", "abc", 5, "abc"},
		{"exact", "abcde", 5, "abcde"},
		{"over", "abcdef", 5, "abcd…"},
		{"no cap", "abcdef", 0, "abcdef"},
		{"one", "abc", 1, "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capMessage(tt.msg, tt.max); got != tt.want {
				t.Errorf("capMessage(%q, %d) = %q, want %q", tt.msg, tt.max, got, tt.want)
			}
		})
	}
}

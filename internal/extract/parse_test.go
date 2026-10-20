package extract

import (
	"strings"
	"testing"
)

func TestParseJSON(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		allowArray bool
		want       string // "" means failure
		wantErr    string // substring of the error
	}{
		{name: "clean", text: `{"a": 1}`, want: `{"a": 1}`},
		{name: "surrounding whitespace", text: "\n  {\"a\": 1}\n", want: `{"a": 1}`},
		{name: "fenced with tag", text: "```json\n{\"a\": 1}\n```", want: `{"a": 1}`},
		{name: "fenced without tag", text: "```\n{\"a\": 1}\n```", want: `{"a": 1}`},
		{name: "prose and fence", text: "Here it is:\n```json\n{\"a\": 1}\n```\nDone.", want: `{"a": 1}`},
		{name: "first of two fences", text: "```\n{\"a\": 1}\n```\n```\n{\"b\": 2}\n```", want: `{"a": 1}`},
		{name: "unclosed fence", text: "```json\n{\"a\": 1}", want: `{"a": 1}`},
		{name: "fence without newline", text: "```{\"a\": 1}", wantErr: "no JSON value"},
		{name: "prose before and after", text: `Sure! {"a": {"b": "}"}} Hope this helps.`, want: `{"a": {"b": "}"}}`},
		{name: "braces and escapes in strings", text: `x {"a": "\"{[", "b": "\\"} y`, want: `{"a": "\"{[", "b": "\\"}`},
		{name: "fence inside a string", text: "{\"a\": \"```\"}", want: "{\"a\": \"```\"}"},
		{name: "two objects take the first", text: `{"a": 1} {"b": 2}`, want: `{"a": 1}`},
		{name: "truncated", text: `{"a": {"b": 1}`, wantErr: "incomplete"},
		{name: "truncated in prose", text: `Result: {"a": [1, 2`, wantErr: "offset 8 is incomplete"},
		{name: "invalid inside", text: `{"a": 1,}`, wantErr: "invalid JSON at offset 9"},
		{name: "invalid after prose", text: `ok {'a': 1}`, wantErr: "invalid JSON at offset 5"},
		{name: "empty", text: "", wantErr: "empty"},
		{name: "blank", text: " \n\t", wantErr: "empty"},
		{name: "refusal", text: "I'm sorry, I can't help with that.", wantErr: "no JSON value"},
		{name: "scalar", text: `"just text"`, wantErr: "no JSON value"},
		{name: "number", text: `42`, wantErr: "no JSON value"},
		{name: "array refused", text: `[{"a": 1}]`, wantErr: "is an array"},
		{name: "array allowed", text: `[{"a": 1}]`, allowArray: true, want: `[{"a": 1}]`},
		{name: "array in prose allowed", text: `List: [1, 2] end`, allowArray: true, want: `[1, 2]`},
		{name: "object when arrays allowed", text: `{"a": 1}`, allowArray: true, want: `{"a": 1}`},
		// Answer blocks (ADR 0012).
		{name: "blocks", text: "<analysis>\n/a: \"1\"\n</analysis>\n<json>\n{\"a\": 1}\n</json>", want: `{"a": 1}`},
		{name: "json block without analysis", text: `<json>{"a": 1}</json>`, want: `{"a": 1}`},
		{name: "braces in the analysis", text: "<analysis>/a: \"{\"b\": 2}\" and [x]</analysis>\n<json>{\"a\": 1}</json>", want: `{"a": 1}`},
		{name: "fence inside the json block", text: "<analysis>x</analysis><json>\n```json\n{\"a\": 1}\n```\n</json>", want: `{"a": 1}`},
		{name: "prose after the json block", text: "<analysis>x</analysis><json>{\"a\": 1}</json>\nDone.", want: `{"a": 1}`},
		{name: "array in the json block", text: "<analysis>x</analysis><json>[1]</json>", allowArray: true, want: `[1]`},
		{name: "json tag quoted in the analysis", text: "<analysis>I answer in <json> next</analysis><json>{\"a\": 1}</json>", want: `{"a": 1}`},
		{name: "two json blocks", text: "<json>{\"a\": 1}</json>\n<json>{\"a\": 2}</json>", wantErr: "more than one <json> block"},
		{name: "unclosed json block", text: "<analysis>x</analysis><json>{\"a\": 1}", wantErr: "<json> block is not closed"},
		{name: "cut off in the analysis", text: "<analysis>/a: {\"a\": 1}", wantErr: "<analysis> block is not closed"},
		{name: "analysis without json block", text: "<analysis>x</analysis> {\"a\": 1}", wantErr: "no <json> block follows"},
		{name: "empty json block", text: "<analysis>x</analysis><json> </json>", wantErr: "<json> block is empty"},
		{name: "invalid json in the block", text: "<analysis>x</analysis><json>{\"a\": 1,}</json>", wantErr: "invalid JSON at offset 37"},
		{name: "array in the block refused", text: "<json>[1]</json>", wantErr: "is an array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJSON(tt.text, tt.allowArray)
			if tt.want != "" {
				if err != nil || string(got) != tt.want {
					t.Fatalf("parseJSON(%q) = %q, %v; want %q", tt.text, got, err, tt.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseJSON(%q) = %q, %v; want error containing %q", tt.text, got, err, tt.wantErr)
			}
		})
	}
}

func FuzzParseJSON(f *testing.F) {
	for _, s := range []string{
		`{"a": 1}`, "```json\n{}\n```", `x {"a": [1, 2]} y`, `{"a": `, `[1]`,
		"<analysis>/a: {\"x\": 1}</analysis><json>{\"a\": 1}</json>", "<analysis>{", "<json>{}</json><json>[]</json>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got, err := parseJSON(text, true)
		if err != nil {
			return
		}
		// Never repaired: the value is valid JSON and a substring of the answer.
		if !strings.Contains(text, string(got)) {
			t.Fatalf("parseJSON(%q) = %q, not part of the answer", text, got)
		}
		if got[0] != '{' && got[0] != '[' {
			t.Fatalf("parseJSON(%q) = %q, not an object or array", text, got)
		}
	})
}

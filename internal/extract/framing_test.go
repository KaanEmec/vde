package extract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/provider"
)

func TestFramings_domainNeutral(t *testing.T) {
	deny := []string{"travel", "flight", "hotel", "invoice", "patient", "{{"}
	for _, schemaless := range []bool{false, true} {
		extractor, judge := framings(schemaless)
		for name, text := range map[string]string{"extractor": extractor, "judge": judge, "reminder": reaskReminder} {
			for _, word := range deny {
				if strings.Contains(strings.ToLower(text), word) {
					t.Errorf("%s framing (schemaless=%v) contains %q", name, schemaless, word)
				}
			}
		}
		// The fake provider tells the roles apart by these words in the system prompt.
		if !strings.Contains(extractor, "extractor") || strings.Contains(extractor, "judge") {
			t.Errorf("extractor framing must name the extractor and not the judge")
		}
		if !strings.Contains(judge, "judge") || strings.Contains(judge, "extractor") {
			t.Errorf("judge framing must name the judge and not the extractor")
		}
	}
}

func TestFramings_modes(t *testing.T) {
	extractor, judge := framings(false)
	if !strings.Contains(extractor, shapeSchema) || !strings.Contains(judge, scopeSchema) {
		t.Error("schema mode framings miss their mode lines")
	}
	extractor, judge = framings(true)
	if !strings.Contains(extractor, shapeSchemaless) || !strings.Contains(judge, scopeSchemaless) {
		t.Error("schema-less framings miss their mode lines")
	}
	if !strings.Contains(judge, `"verdict"`) || !strings.Contains(judge, `"confidence"`) || !strings.Contains(judge, "RFC 6901") {
		t.Error("judge framing does not define the verdict format")
	}
}

// TestFramings_order: each framing lists the request's sections in the order the
// assemblers send them, document first (ADR 0012).
func TestFramings_order(t *testing.T) {
	for _, schemaless := range []bool{false, true} {
		extractor, judge := framings(schemaless)
		for name, tc := range map[string]struct {
			text string
			seps []string
		}{
			"extractor": {extractor, []string{sepDocument, sepSchema, sepInstructions, sepPrevious, sepFeedback}},
			"judge":     {judge, []string{sepDocument, sepSchema, sepInstructions, sepCandidate}},
		} {
			at := -1
			for _, sep := range tc.seps {
				i := strings.Index(tc.text, `"`+sep+`"`)
				if i < at {
					t.Errorf("%s framing (schemaless=%v): %q is missing or out of order", name, schemaless, sep)
				}
				at = i
			}
		}
	}
}

// testInput has instructions with leading and trailing whitespace and markdown, which
// must reach the models byte for byte.
func testInput(t *testing.T, schema *Schema) *Input {
	t.Helper()
	return &Input{
		Representation: document.Representation{
			Kind: document.Pages,
			Parts: []provider.Part{
				{Image: &provider.Blob{MediaType: "image/png", Data: []byte("page 1")}},
				{Image: &provider.Blob{MediaType: "image/png", Data: []byte("page 2")}},
			},
			MediaTypes: []string{"image/png"},
		},
		ExtractorInstructions: "  # Extract\n\nEvery *field*.\n\n",
		JudgeInstructions:     "\tCheck it all. ",
		Schema:                schema,
	}
}

func TestAssembleExtractor(t *testing.T) {
	schema, err := CompileSchema([]byte(` {"type": "object"} `))
	if err != nil {
		t.Fatal(err)
	}
	in := testInput(t, schema)
	feedback := []Feedback{{Pointer: "/a", Message: "wrong", Source: SourceJudge}, {Pointer: "", Message: "also", Source: SourceSchema}}

	tests := []struct {
		name     string
		in       *Input
		prev     json.RawMessage
		feedback []Feedback
		want     []string // text parts; "<doc>" stands for the document parts
	}{
		{
			name: "first attempt",
			in:   in,
			want: []string{"Document:", "<doc>", "Schema:\n\n" + ` {"type": "object"} `, "Caller instructions:\n\n" + in.ExtractorInstructions},
		},
		{
			name:     "retry",
			in:       in,
			prev:     json.RawMessage(`{"a": 1}`),
			feedback: feedback,
			want: []string{
				"Document:", "<doc>", "Schema:\n\n" + ` {"type": "object"} `, "Caller instructions:\n\n" + in.ExtractorInstructions,
				"Previous candidate:\n\n{\"a\": 1}", "Feedback:\n\n- \"/a\" (judge): wrong\n- \"\" (schema): also",
			},
		},
		{
			name:     "retry after an answer that was not JSON",
			in:       testInput(t, nil),
			feedback: feedback[1:],
			want:     []string{"Document:", "<doc>", "Caller instructions:\n\n" + in.ExtractorInstructions, "Feedback:\n\n- \"\" (schema): also"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertParts(t, assembleExtractor(tt.in, tt.prev, tt.feedback), tt.want, tt.in.Representation.Parts)
		})
	}
}

func TestAssembleJudge(t *testing.T) {
	in := testInput(t, nil)
	candidate := json.RawMessage(`{"a": 1}`)
	base := []string{"Document:", "<doc>", "Caller instructions:\n\n" + in.JudgeInstructions, "Candidate:\n\n{\"a\": 1}"}
	assertParts(t, assembleJudge(in, candidate, false), base, in.Representation.Parts)
	schema, err := CompileSchema([]byte(`{"type": "object"}`))
	if err != nil {
		t.Fatal(err)
	}
	withSchema := testInput(t, schema)
	assertParts(t, assembleJudge(withSchema, candidate, false),
		[]string{"Document:", "<doc>", "Schema:\n\n" + `{"type": "object"}`, "Caller instructions:\n\n" + in.JudgeInstructions, "Candidate:\n\n{\"a\": 1}"},
		withSchema.Representation.Parts)
	assertParts(t, assembleJudge(in, candidate, true), append(base, reaskReminder), in.Representation.Parts)
}

// assertParts checks parts against want, where "<doc>" stands for the document parts,
// which must be the very same parts (same blobs).
func assertParts(t *testing.T, parts []provider.Part, want []string, doc []provider.Part) {
	t.Helper()
	i := 0
	for _, w := range want {
		if w == "<doc>" {
			if i+len(doc) > len(parts) || !reflect.DeepEqual(parts[i:i+len(doc)], doc) {
				t.Fatalf("parts[%d:] are not the document parts: %+v", i, parts)
			}
			for j := range doc {
				if parts[i+j].Image != doc[j].Image {
					t.Fatalf("document part %d is a copy, want the same blob", j)
				}
			}
			i += len(doc)
			continue
		}
		if i >= len(parts) || parts[i].Text != w {
			t.Fatalf("part %d = %+v, want text %q", i, parts[min(i, len(parts)-1)], w)
		}
		i++
	}
	if i != len(parts) {
		t.Fatalf("%d parts, want %d", len(parts), i)
	}
}

package document

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/ground"
)

func mailWithDate(date string) []byte {
	head := "From: a@example.com\nSubject: Hi\n"
	if date != "" {
		head += "Date: " + date + "\n"
	}
	return []byte(head + "\nBody\n")
}

func TestContextLine(t *testing.T) {
	t.Parallel()
	textOnly := []string{"text/plain"}
	tests := []struct {
		name string
		in   Input
		want string
	}{
		{"plain text", Input{MediaType: "text/plain", Data: []byte("hello")}, "type: text"},
		{"markdown", Input{MediaType: "text/markdown", Data: []byte("# hi")}, "type: markdown"},
		{"html", Input{MediaType: "text/html", Data: []byte("<p>hi</p>")}, "type: html"},
		{"email keeps the offset", Input{MediaType: "message/rfc822", Data: mailWithDate("Sat, 10 Oct 2026 09:14:00 +0200")},
			"type: email · sent: 2026-10-10T09:14:00+02:00"},
		{"email with a negative offset", Input{MediaType: "message/rfc822", Data: mailWithDate("Sat, 10 Oct 2026 09:14:00 -0500 (CDT)")},
			"type: email · sent: 2026-10-10T09:14:00-05:00"},
		{"email in UTC", Input{MediaType: "message/rfc822", Data: mailWithDate("Sat, 10 Oct 2026 09:14:00 GMT")},
			"type: email · sent: 2026-10-10T09:14:00Z"},
		{"email without a Date header", Input{MediaType: "message/rfc822", Data: mailWithDate("")}, "type: email"},
		{"email with an unparseable Date header", Input{MediaType: "message/rfc822", Data: mailWithDate("last Tuesday, about nine")},
			"type: email"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(tt.in, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			rep, err := Select(t.Context(), doc, capsFor(t, textOnly), capsFor(t, textOnly), "", ImagePolicy{}, PDFPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			if line, _, _ := strings.Cut(rep.Parts[0].Text, "\n"); line != tt.want {
				t.Errorf("context line = %q, want %q", line, tt.want)
			}
			// The line is never grounding text, and Document.Text is what it was.
			if strings.Contains(rep.GroundText, "type: ") {
				t.Errorf("GroundText has the context line")
			}
			if text, _ := doc.Text(""); strings.HasPrefix(text, "type: ") {
				t.Errorf("Document.Text has the context line")
			}
		})
	}
}

func TestContextLine_images(t *testing.T) {
	t.Parallel()
	vision := capsFor(t, []string{"image/png", "image/jpeg"})
	tests := []struct {
		name  string
		in    Input
		kind  Kind
		parts []string // text of each part, "" for a binary part
	}{
		{"native image", Input{MediaType: "image/png", Data: pngData}, Native, []string{"type: image", ""}},
		{"native pdf", Input{MediaType: "application/pdf", Data: pdfData}, Native, []string{"type: pdf", ""}},
		{"caller pages", Input{MediaType: "application/pdf", Data: pdfData, Pages: []Page{{Data: pngData}, {Data: jpegData}, {Data: pngData}}},
			Pages, []string{"type: pdf · pages: 3", "--- page 1 ---", "", "--- page 2 ---", "", "--- page 3 ---", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(tt.in, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			caps := vision
			if tt.name == "native pdf" {
				caps = capsFor(t, []string{"application/pdf"})
			}
			rep, err := Select(t.Context(), doc, caps, caps, "", ImagePolicy{}, PDFPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(rep.Parts))
			for i, p := range rep.Parts {
				got[i] = p.Text
			}
			if rep.Kind != tt.kind || !reflect.DeepEqual(got, tt.parts) {
				t.Errorf("%s parts %q, want %s %q", rep.Kind, got, tt.kind, tt.parts)
			}
			if rep.Groundable || rep.GroundText != "" {
				t.Error("images and page markers are not grounding text")
			}
		})
	}
}

func TestContextLine_renderedPages(t *testing.T) {
	t.Parallel()
	doc, err := New(Input{MediaType: "application/pdf", Data: pdfData}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubPDF{texts: []string{richPage, thinPage}}
	vision := capsFor(t, []string{"image/png"})
	rep, err := Select(t.Context(), doc, vision, vision, "", ImagePolicy{}, stubPolicy("pages", stub))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range rep.Parts {
		switch {
		case p.Image != nil:
			order = append(order, "image")
		default:
			order = append(order, p.Text)
		}
	}
	want := []string{"type: pdf · pages: 2", "--- page 1 ---", "image", "--- page 2 ---", "image"}
	if rep.Source != SourceRendered || !reflect.DeepEqual(order, want) {
		t.Errorf("%s parts %q, want %q", rep.Label(), order, want)
	}
}

// TestContextLine_notGrounding: a value that exists only in what the service added (the
// context line, a page marker) is a miss when grounding reads rep.GroundText.
func TestContextLine_notGrounding(t *testing.T) {
	t.Parallel()
	pages := make([]string, 17)
	for i := range pages {
		pages[i] = richPage
	}
	pdfDoc, err := New(Input{MediaType: "application/pdf", Data: pdfData}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	textOnly := capsFor(t, []string{"text/plain"})
	pdfRep, err := Select(t.Context(), pdfDoc, textOnly, textOnly, "", ImagePolicy{}, stubPolicy("text", &stubPDF{texts: pages}))
	if err != nil {
		t.Fatal(err)
	}
	mdDoc, err := New(Input{MediaType: "text/markdown", Data: []byte("Booking HV-48213, total 412.50 EUR")}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	mdRep, err := Select(t.Context(), mdDoc, textOnly, textOnly, "", ImagePolicy{}, PDFPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		rep       Representation
		candidate string
		line      string // must be in the model text
		want      []string
	}{
		{"page count only in the line", pdfRep, `{"pages":17,"ref":"HV-48213"}`, "pages: 17", []string{"/pages"}},
		{"document type only in the line", mdRep, `{"kind":"markdown","ref":"HV-48213"}`, "type: markdown", []string{"/kind"}},
		{"a value in the document is grounded", mdRep, `{"ref":"HV-48213","total":412.5}`, "type: markdown", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tt.rep.Parts[0].Text, tt.line) {
				t.Fatalf("the model text lacks %q", tt.line)
			}
			if !tt.rep.Groundable {
				t.Fatal("not groundable")
			}
			res := ground.Check(ground.NewText(tt.rep.GroundText), json.RawMessage(tt.candidate), nil, 0)
			got := []string{}
			for _, m := range res.Ungrounded {
				got = append(got, m.Pointer)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ungrounded = %v, want %v", got, tt.want)
			}
		})
	}
}

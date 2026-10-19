package document

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// capsFor returns the capabilities of a group with one endpoint-model pair per accepts
// list, as config computes them.
func capsFor(t *testing.T, accepts ...[]string) []config.Capability {
	t.Helper()
	ep := &config.Endpoint{Provider: "fake", Models: map[string]config.Model{}}
	group := &config.Group{}
	for i, a := range accepts {
		name := "m" + strconv.Itoa(i)
		ep.Models[name] = config.Model{Accepts: a}
		group.Endpoints = append(group.Endpoints, config.GroupEntry{Endpoint: "e", Model: name})
	}
	cfg := &config.Config{
		Endpoints: map[string]*config.Endpoint{"e": ep},
		Groups:    map[string]*config.Group{"g": group},
	}
	return cfg.Capabilities("g")
}

func TestSelect(t *testing.T) {
	t.Parallel()
	var (
		pdfNative  = []string{"application/pdf", "image/png", "image/jpeg", "text/plain"}
		vision     = []string{"image/png", "image/jpeg", "text/plain"}
		pngOnly    = []string{"image/png", "text/plain"}
		jpegOnly   = []string{"image/jpeg", "text/plain"}
		textOnly   = []string{"text/plain"}
		htmlNoText = []string{"text/html", "application/pdf"}
	)
	pages := []Page{{Data: pngData}, {Data: pngData}}
	mixed := []Page{{Data: pngData}, {Data: jpegData}}
	html := []byte(`<html><head><style>b{}</style></head><body><p>Hi <a href="https://x.example">there</a></p></body></html>`)
	tests := []struct {
		name       string
		in         Input
		extractor  [][]string
		judge      [][]string
		policy     HTMLPolicy
		wantKind   Kind
		wantTypes  []string
		wantText   string
		wantPolicy HTMLPolicy
		wantErr    bool
	}{
		{name: "pdf native", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: pages},
			extractor: [][]string{pdfNative}, judge: [][]string{pdfNative},
			wantKind: Native, wantTypes: []string{"application/pdf"}},
		{name: "pdf pages", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: pages},
			extractor: [][]string{vision}, judge: [][]string{vision},
			wantKind: Pages, wantTypes: []string{"image/png"}},
		{name: "pdf without pages or native", in: Input{MediaType: "application/pdf", Data: pdfData},
			extractor: [][]string{vision}, judge: [][]string{vision}, wantErr: true},
		{name: "judge forces pages", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: pages},
			extractor: [][]string{pdfNative}, judge: [][]string{vision},
			wantKind: Pages, wantTypes: []string{"image/png"}},
		{name: "judge reads nothing", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: pages},
			extractor: [][]string{pdfNative}, judge: [][]string{textOnly}, wantErr: true},
		{name: "extractor pages only, judge native only", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: pages},
			extractor: [][]string{vision}, judge: [][]string{{"application/pdf"}}, wantErr: true},
		{name: "image native", in: Input{MediaType: "image/jpeg", Data: jpegData},
			extractor: [][]string{vision}, judge: [][]string{vision},
			wantKind: Native, wantTypes: []string{"image/jpeg"}},
		{name: "heic unread falls to pages", in: Input{MediaType: "image/heic", Data: heicData, Pages: []Page{{Data: jpegData}}},
			extractor: [][]string{vision}, judge: [][]string{vision},
			wantKind: Pages, wantTypes: []string{"image/jpeg"}},
		{name: "heic unread no pages", in: Input{MediaType: "image/heic", Data: heicData},
			extractor: [][]string{vision}, judge: [][]string{vision}, wantErr: true},
		{name: "mixed pages need one pair reading both", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: mixed},
			extractor: [][]string{pngOnly, jpegOnly}, judge: [][]string{vision}, wantErr: true},
		{name: "mixed pages", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: mixed},
			extractor: [][]string{pngOnly, vision}, judge: [][]string{vision},
			wantKind: Pages, wantTypes: []string{"image/png", "image/jpeg"}},
		{name: "plain text", in: Input{MediaType: "text/plain", Data: []byte("hello")},
			extractor: [][]string{textOnly}, judge: [][]string{textOnly},
			wantKind: Text, wantTypes: []string{"text/plain"}, wantText: "hello"},
		{name: "markdown", in: Input{MediaType: "text/markdown", Data: []byte("# hi")},
			extractor: [][]string{textOnly}, judge: [][]string{textOnly},
			wantKind: Text, wantTypes: []string{"text/plain"}, wantText: "# hi"},
		{name: "html raw default", in: Input{MediaType: "text/html", Data: html},
			extractor: [][]string{textOnly}, judge: [][]string{textOnly},
			wantKind: Text, wantTypes: []string{"text/plain"}, wantPolicy: RawHTML,
			wantText: `<html><head><style></style></head><body><p>Hi <a href="https://x.example">there</a></p></body></html>`},
		{name: "html visible text", in: Input{MediaType: "text/html", Data: html}, policy: VisibleText,
			extractor: [][]string{textOnly}, judge: [][]string{textOnly},
			wantKind: Text, wantTypes: []string{"text/plain"}, wantPolicy: VisibleText,
			wantText: "Hi there (https://x.example)"},
		{name: "html without text reader", in: Input{MediaType: "text/html", Data: html},
			extractor: [][]string{htmlNoText}, judge: [][]string{textOnly}, wantErr: true},
		{name: "email", in: Input{MediaType: "message/rfc822", Data: []byte("From: a@example.com\nSubject: Hi\n\nBody\n")},
			extractor: [][]string{textOnly}, judge: [][]string{textOnly},
			wantKind: Text, wantTypes: []string{"text/plain"}, wantText: "From: a@example.com\nSubject: Hi\n\nBody\n"},
		{name: "empty groups", in: Input{MediaType: "text/plain", Data: []byte("x")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(tt.in, testLimits)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			r, err := Select(t.Context(), doc, capsFor(t, tt.extractor...), capsFor(t, tt.judge...), tt.policy, ImagePolicy{}, PDFPolicy{})
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedDocument) {
					t.Fatalf("error = %v, want unsupported_document", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select: %v", err)
			}
			if r.Kind != tt.wantKind || !reflect.DeepEqual(r.MediaTypes, tt.wantTypes) || r.HTMLPolicy != tt.wantPolicy {
				t.Errorf("got %s %v %q, want %s %v %q", r.Kind, r.MediaTypes, r.HTMLPolicy, tt.wantKind, tt.wantTypes, tt.wantPolicy)
			}
			switch r.Kind {
			case Native:
				blob := r.Parts[0].Document
				if doc.Category == CategoryImage {
					blob = r.Parts[0].Image
				}
				if len(r.Parts) != 1 || blob == nil || !bytes.Equal(blob.Data, doc.Data) || blob.MediaType != tt.wantTypes[0] {
					t.Errorf("native parts = %+v", r.Parts)
				}
			case Pages:
				if len(r.Parts) != len(doc.Pages) {
					t.Fatalf("got %d parts, want %d", len(r.Parts), len(doc.Pages))
				}
				for i, p := range r.Parts {
					if p.Image == nil || !bytes.Equal(p.Image.Data, doc.Pages[i].Data) {
						t.Errorf("part %d is not page %d", i, i+1)
					}
				}
			case Text:
				if len(r.Parts) != 1 || r.Parts[0].Text != tt.wantText {
					t.Errorf("text = %q, want %q", r.Parts[0].Text, tt.wantText)
				}
			}
		})
	}
}

// recorder is a provider that records the parts of every request.
type recorder struct{ seen [][]provider.Part }

func (r *recorder) Complete(_ context.Context, req provider.Request) (provider.Response, error) {
	r.seen = append(r.seen, req.Parts)
	return provider.Response{Text: "{}"}, nil
}

// TestSelectSamePartsForBothRoles checks that the extractor and the judge are given
// byte-identical document parts built once from the same representation.
func TestSelectSamePartsForBothRoles(t *testing.T) {
	t.Parallel()
	caps := capsFor(t, []string{"application/pdf", "image/png", "text/plain"})
	inputs := []Input{
		{MediaType: "application/pdf", Data: pdfData},
		{MediaType: "application/pdf", Data: pdfData, Pages: []Page{{Data: pngData}}},
		{MediaType: "message/rfc822", Data: readFile(t, "testdata/eml/mixed-pdf.eml")},
	}
	for _, in := range inputs {
		doc, err := New(in, testLimits)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Select(t.Context(), doc, caps, caps, VisibleText, ImagePolicy{}, PDFPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		rec := &recorder{}
		for _, system := range []string{"extractor framing", "judge framing"} {
			if _, err := rec.Complete(t.Context(), provider.Request{System: system, Parts: r.Parts}); err != nil {
				t.Fatal(err)
			}
		}
		extractor, judge := rec.seen[0], rec.seen[1]
		if len(extractor) != len(judge) {
			t.Fatalf("%s: part counts differ", in.MediaType)
		}
		for i := range extractor {
			if !samePart(extractor[i], judge[i]) {
				t.Errorf("%s: part %d differs between roles", in.MediaType, i)
			}
		}
		again, err := Select(t.Context(), doc, caps, caps, VisibleText, ImagePolicy{}, PDFPolicy{})
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Errorf("%s: Select is not deterministic", in.MediaType)
		}
	}
}

func samePart(a, b provider.Part) bool {
	sameBlob := func(x, y *provider.Blob) bool {
		return (x == nil) == (y == nil) && (x == nil || x.MediaType == y.MediaType && bytes.Equal(x.Data, y.Data))
	}
	return a.Text == b.Text && sameBlob(a.Image, b.Image) && sameBlob(a.Document, b.Document)
}

func TestHTMLPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		global, app string
		want        HTMLPolicy
		wantErr     bool
	}{
		{"", "", RawHTML, false},
		{"raw_html", "", RawHTML, false},
		{"visible_text", "", VisibleText, false},
		{"raw_html", "visible_text", VisibleText, false},
		{"visible_text", "raw_html", RawHTML, false},
		{"markdown", "", "", true},
	}
	for _, tt := range tests {
		got, err := ResolveHTMLPolicy(tt.global, tt.app)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("ResolveHTMLPolicy(%q, %q) = %q, %v", tt.global, tt.app, got, err)
		}
	}
	if HTMLPolicy("").String() != "raw_html" || Native.String() != "native" || Pages.String() != "pages" || Text.String() != "text" {
		t.Error("names for service_version changed")
	}
	doc, err := New(Input{MediaType: "text/plain", Data: []byte("x")}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Select(t.Context(), doc, nil, nil, "bogus", ImagePolicy{}, PDFPolicy{}); err == nil {
		t.Error("Select accepted an unknown policy")
	}
}

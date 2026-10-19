package document

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
)

// testLimits are the contract §4 limits, as the configuration defaults set them; callers
// always pass validated, positive limits.
var testLimits = config.Limits{DocumentBytes: 25 << 20, PageBytes: 10 << 20, Pages: 20}

// Minimal signatures http.DetectContentType recognizes.
var (
	pngData  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpegData = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	pdfData  = []byte("%PDF-1.7\n%secret-content\n")
	webpData = []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	heicData = []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00")
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func wantCode(t *testing.T, err error, code Code) {
	t.Helper()
	var de *Error
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestNewAcceptedTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		data        []byte
		wantType    string
		wantCat     Category
	}{
		{"pdf", "application/pdf", pdfData, "application/pdf", CategoryPDF},
		{"jpeg", "image/jpeg", jpegData, "image/jpeg", CategoryImage},
		{"jpg alias", "image/jpg", jpegData, "image/jpeg", CategoryImage},
		{"png", "image/png", pngData, "image/png", CategoryImage},
		{"webp", "image/webp", webpData, "image/webp", CategoryImage},
		{"heic", "image/heic", heicData, "image/heic", CategoryImage},
		{"binary params dropped", "Application/PDF; name=x.pdf", pdfData, "application/pdf", CategoryPDF},
		{"html", "text/html", []byte("<p>hi</p>"), "text/html; charset=utf-8", CategoryText},
		{"html case", "Text/HTML; Charset=UTF-8", []byte("<p>hi</p>"), "text/html; charset=utf-8", CategoryText},
		{"plain", "text/plain", []byte("hello"), "text/plain; charset=utf-8", CategoryText},
		{"markdown", "text/markdown; charset=utf-8", []byte("# hi"), "text/markdown; charset=utf-8", CategoryText},
		{"email", "message/rfc822", []byte("From: a@example.com\n\nbody\n"), "message/rfc822", CategoryEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(Input{MediaType: tt.contentType, Data: tt.data, Filename: "f"}, testLimits)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if doc.MediaType != tt.wantType || doc.Category != tt.wantCat || doc.Filename != "f" {
				t.Errorf("got %q %s %q, want %q %s", doc.MediaType, doc.Category, doc.Filename, tt.wantType, tt.wantCat)
			}
		})
	}
}

func TestNewRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		data        []byte
		limits      config.Limits
		want        Code
	}{
		{"unknown type", "application/zip", []byte("PK\x03\x04"), testLimits, CodeUnsupportedMediaType},
		{"unreadable type", "text/", []byte("x"), testLimits, CodeUnsupportedMediaType},
		{"empty type", "", []byte("x"), testLimits, CodeUnsupportedMediaType},
		{"png declared as pdf", "application/pdf", pngData, testLimits, CodeUnsupportedMediaType},
		{"pdf declared as png", "image/png", pdfData, testLimits, CodeUnsupportedMediaType},
		{"jpeg declared as heic", "image/heic", jpegData, testLimits, CodeUnsupportedMediaType},
		{"invalid utf-8 undeclared", "text/plain", []byte("caf\xe9"), testLimits, CodeBadRequest},
		{"invalid utf-8 declared", "text/markdown; charset=utf-8", []byte("caf\xe9"), testLimits, CodeBadRequest},
		{"unknown charset", "text/plain; charset=x-klingon", []byte("x"), testLimits, CodeUnsupportedMediaType},
		{"empty", "text/plain", nil, testLimits, CodeBadRequest},
		{"too large", "text/plain", []byte("12345678901"), config.Limits{DocumentBytes: 10, PageBytes: 10 << 20, Pages: 20}, CodePayloadTooLarge},
		{"email without body", "message/rfc822", []byte("From: a@example.com\nContent-Type: image/png\n\nx"), testLimits, CodeUnsupportedDocument},
		{"email garbage", "message/rfc822", []byte("not an email header line\x00\n"), testLimits, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Input{MediaType: tt.contentType, Data: tt.data}, tt.limits)
			wantCode(t, err, tt.want)
		})
	}
}

func TestNewTextCharsets(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("<!-- padding -->", 100) // pushes non-ASCII past the 1024-byte prescan
	tests := []struct {
		name        string
		contentType string
		data        []byte
		want        string
	}{
		{"declared latin1", "text/plain; charset=iso-8859-1", []byte("caf\xe9"), "café"},
		{"declared windows-1252", "text/plain; charset=windows-1252", []byte("120 \x80"), "120 €"},
		{"declared shift_jis", "text/plain; charset=shift_jis", []byte("\x93\xfa\x96\x7b"), "日本"},
		{"us-ascii is utf-8", "text/plain; charset=us-ascii", []byte("plain"), "plain"},
		{"utf-8 bom stripped", "text/plain", []byte("\xef\xbb\xbfhi"), "hi"},
		{"html meta charset", "text/html", []byte(`<meta charset="iso-8859-1"><p>caf` + "\xe9"), `<meta charset="iso-8859-1"><p>café`},
		{"html header wins over meta", "text/html; charset=utf-8", []byte(`<meta charset="iso-8859-1">café`), `<meta charset="iso-8859-1">café`},
		{"html utf-8 after prescan window", "text/html", []byte(long + "café"), long + "café"},
		{"html undeclared non-utf-8", "text/html", []byte("caf\xe9"), "café"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(Input{MediaType: tt.contentType, Data: tt.data}, testLimits)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if string(doc.Data) != tt.want {
				t.Errorf("Data = %q, want %q", doc.Data, tt.want)
			}
			if !strings.HasSuffix(doc.MediaType, "; charset=utf-8") {
				t.Errorf("MediaType = %q, want charset=utf-8", doc.MediaType)
			}
		})
	}
}

func TestNewPages(t *testing.T) {
	t.Parallel()
	page := func(mt string, data []byte) Page { return Page{MediaType: mt, Data: data} }
	many := func(n int) []Page {
		out := make([]Page, n)
		for i := range out {
			out[i] = page("", pngData)
		}
		return out
	}
	tests := []struct {
		name   string
		pages  []Page
		limits config.Limits
		want   Code // "" for success
	}{
		{"png and jpeg", []Page{page("image/png", pngData), page("", jpegData)}, testLimits, ""},
		{"at the count limit", many(testLimits.Pages), testLimits, ""},
		{"over the count limit", many(testLimits.Pages + 1), testLimits, CodePayloadTooLarge},
		{"over configured count", many(3), config.Limits{DocumentBytes: 25 << 20, PageBytes: 10 << 20, Pages: 2}, CodePayloadTooLarge},
		{"page too large", []Page{page("", pngData)}, config.Limits{DocumentBytes: 25 << 20, PageBytes: 4, Pages: 20}, CodePayloadTooLarge},
		{"pdf as page", []Page{page("", pdfData)}, testLimits, CodeUnsupportedMediaType},
		{"webp as page", []Page{page("image/webp", webpData)}, testLimits, CodeUnsupportedMediaType},
		{"declared mismatch", []Page{page("image/jpeg", pngData)}, testLimits, CodeUnsupportedMediaType},
		{"empty page", []Page{page("image/png", nil)}, testLimits, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(Input{MediaType: "application/pdf", Data: pdfData, Pages: tt.pages}, tt.limits)
			if tt.want != "" {
				wantCode(t, err, tt.want)
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if len(doc.Pages) != len(tt.pages) {
				t.Fatalf("got %d pages, want %d", len(doc.Pages), len(tt.pages))
			}
			for i, p := range doc.Pages {
				if !bytes.Equal(p.Data, tt.pages[i].Data) {
					t.Errorf("page %d out of order", i+1)
				}
			}
		})
	}
	doc, err := New(Input{MediaType: "application/pdf", Data: pdfData, Pages: []Page{page("", jpegData), page("", pngData)}}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Pages[0].MediaType != "image/jpeg" || doc.Pages[1].MediaType != "image/png" {
		t.Errorf("page media types = %q, %q", doc.Pages[0].MediaType, doc.Pages[1].MediaType)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	_, err := New(Input{MediaType: "image/png", Data: pdfData}, testLimits)
	if !errors.Is(err, ErrUnsupportedMediaType) || errors.Is(err, ErrBadRequest) {
		t.Errorf("errors.Is mismatch for %v", err)
	}
	if strings.Contains(err.Error(), "secret-content") || strings.Contains(err.Error(), "PDF-1.7") {
		t.Errorf("error leaks content: %v", err)
	}
	_, err = New(Input{MediaType: "text/plain", Data: []byte("secret-content \xff")}, testLimits)
	if !errors.Is(err, ErrBadRequest) || strings.Contains(err.Error(), "secret-content") {
		t.Errorf("error = %v", err)
	}
}

func TestMediaTypes(t *testing.T) {
	t.Parallel()
	required := []string{"application/pdf", "image/jpeg", "image/png", "text/html", "text/plain"}
	var names []string
	for _, mt := range MediaTypes() {
		names = append(names, mt.Name)
		if mt.Required != slices.Contains(required, mt.Name) {
			t.Errorf("%s: Required = %v", mt.Name, mt.Required)
		}
	}
	for _, want := range append(required, "image/webp", "image/heic", "text/markdown", "message/rfc822") {
		if !slices.Contains(names, want) {
			t.Errorf("missing %s", want)
		}
	}
	// Every type configuration knows must be accepted here.
	cfg := &config.Config{Groups: map[string]*config.Group{"g": {}}}
	for _, c := range cfg.Capabilities("g") {
		if !slices.Contains(names, c.MediaType) {
			t.Errorf("config type %s is not accepted", c.MediaType)
		}
	}
}

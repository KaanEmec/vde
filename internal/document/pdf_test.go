package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/pdf/pdftest"
)

// stubPDF is a PDFEngine whose documents have the given page texts and render to a
// deterministic gray PNG of w×h pixels.
type stubPDF struct {
	texts   []string
	openErr error
	textErr error
	w, h    int
	opens   atomic.Int32
}

func (s *stubPDF) Open(context.Context, []byte) (PDFDoc, error) {
	s.opens.Add(1)
	if s.openErr != nil {
		return nil, s.openErr
	}
	return stubDoc{s}, nil
}

type stubDoc struct{ s *stubPDF }

func (d stubDoc) PageCount() int { return len(d.s.texts) }
func (d stubDoc) Text(_ context.Context, page int) (string, error) {
	return d.s.texts[page], d.s.textErr
}
func (d stubDoc) Close() {}
func (d stubDoc) Render(_ context.Context, page, dpi int) ([]byte, error) {
	w, h := d.s.w, d.s.h
	if w == 0 {
		w, h = 62, 88
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(page*40 + dpi + i%7)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	richPage = strings.Repeat("Booking HV-48213 total EUR 412.50. ", 8) // 280 characters
	thinPage = "Page 2"
)

func stubPolicy(mode string, e PDFEngine) PDFPolicy {
	return PDFPolicy{Mode: mode, DPI: 150, MaxPages: 20, MinTextChars: 200, Engine: e}
}

func TestSelect_pdfPolicy(t *testing.T) {
	t.Parallel()
	var (
		pdfNative = []string{"application/pdf", "image/png", "image/jpeg", "text/plain"}
		vision    = []string{"image/png", "image/jpeg", "text/plain"}
		imageOnly = []string{"image/png", "image/jpeg"}
		jpegOnly  = []string{"image/jpeg"}
		textOnly  = []string{"text/plain"}
	)
	callerPages := []Page{{Data: pngData}}
	tests := []struct {
		name        string
		texts       []string
		openErr     error
		mode        string
		nilEngine   bool
		pages       []Page
		extractor   []string
		judge       []string
		images      ImagePolicy
		wantKind    Kind
		wantSource  Source
		wantTypes   []string
		wantGround  bool
		wantErr     error // ErrUnsupportedDocument, or nil
		wantMessage string
		wantOpens   int32
		wantFail    bool // a non-document error
	}{
		{name: "auto, text-rich, text-only groups", texts: []string{richPage, richPage}, mode: "auto",
			extractor: textOnly, judge: textOnly,
			wantKind: Text, wantSource: SourceTextLayer, wantTypes: []string{"text/plain"}, wantGround: true, wantOpens: 1},
		{name: "empty mode is auto", texts: []string{richPage}, mode: "",
			extractor: textOnly, judge: textOnly,
			wantKind: Text, wantSource: SourceTextLayer, wantTypes: []string{"text/plain"}, wantGround: true, wantOpens: 1},
		{name: "auto, text-rich, image-only groups", texts: []string{richPage}, mode: "auto",
			extractor: imageOnly, judge: imageOnly,
			wantKind: Pages, wantSource: SourceRendered, wantTypes: []string{"image/png"}, wantGround: true, wantOpens: 1},
		{name: "auto, one blank page renders", texts: []string{richPage, "  \n "}, mode: "auto",
			extractor: vision, judge: vision,
			wantKind: Pages, wantSource: SourceRendered, wantTypes: []string{"image/png"}, wantOpens: 1},
		{name: "auto, thin text, text-only groups refused", texts: []string{thinPage}, mode: "auto",
			extractor: textOnly, judge: textOnly, wantErr: ErrUnsupportedDocument,
			wantMessage: "fewer than 200 characters", wantOpens: 1},
		{name: "text forces text with too little text", texts: []string{thinPage}, mode: "text",
			extractor: vision, judge: vision,
			wantKind: Text, wantSource: SourceTextLayer, wantTypes: []string{"text/plain"}, wantOpens: 1},
		{name: "pages forces pages on a text-rich PDF", texts: []string{richPage}, mode: "pages",
			extractor: vision, judge: vision,
			wantKind: Pages, wantSource: SourceRendered, wantTypes: []string{"image/png"}, wantGround: true, wantOpens: 1},
		{name: "pages with text-only groups refused", texts: []string{richPage}, mode: "pages",
			extractor: textOnly, judge: textOnly, wantErr: ErrUnsupportedDocument, wantOpens: 1},
		{name: "native refuses without pages", texts: []string{richPage}, mode: "native",
			extractor: vision, judge: vision, wantErr: ErrUnsupportedDocument},
		{name: "no engine refuses without pages", texts: []string{richPage}, mode: "auto", nilEngine: true,
			extractor: vision, judge: vision, wantErr: ErrUnsupportedDocument},
		{name: "caller pages win over rendering", texts: []string{richPage}, mode: "auto", pages: callerPages,
			extractor: vision, judge: vision,
			wantKind: Pages, wantSource: SourceGiven, wantTypes: []string{"image/png"}},
		{name: "pdf-capable pair stays native", texts: []string{richPage}, mode: "pages",
			extractor: pdfNative, judge: pdfNative,
			wantKind: Native, wantSource: SourceGiven, wantTypes: []string{"application/pdf"}},
		{name: "roles disagree, both get text", texts: []string{richPage}, mode: "auto",
			extractor: pdfNative, judge: textOnly,
			wantKind: Text, wantSource: SourceTextLayer, wantTypes: []string{"text/plain"}, wantGround: true, wantOpens: 1},
		{name: "too many pages", texts: make([]string, 21), mode: "auto",
			extractor: vision, judge: vision, wantErr: ErrUnsupportedDocument,
			wantMessage: "more than 20 pages (documents.pdf.max_pages)", wantOpens: 1},
		{name: "engine page limit", openErr: pdf.ErrTooManyPages, mode: "auto",
			extractor: vision, judge: vision, wantErr: ErrUnsupportedDocument,
			wantMessage: "more than 20 pages", wantOpens: 1},
		{name: "encrypted", openErr: pdf.ErrEncrypted, mode: "auto",
			extractor: vision, judge: vision, wantErr: ErrUnsupportedDocument,
			wantMessage: "encrypted or invalid", wantOpens: 1},
		{name: "timeout is not a refusal", openErr: pdf.ErrTimeout, mode: "auto",
			extractor: vision, judge: vision, wantFail: true, wantOpens: 1},
		{name: "rendered pages through the image policy", texts: []string{thinPage}, mode: "auto",
			extractor: jpegOnly, judge: jpegOnly,
			images:   ImagePolicy{Enabled: true, MaxDimension: 40, ConvertTo: "image/jpeg"},
			wantKind: Pages, wantSource: SourceRendered, wantTypes: []string{"image/jpeg"}, wantOpens: 1},
		{name: "rendered PNG unreadable without conversion", texts: []string{thinPage}, mode: "auto",
			extractor: jpegOnly, judge: jpegOnly, wantErr: ErrUnsupportedDocument, wantOpens: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(Input{MediaType: "application/pdf", Data: pdfData, Pages: tt.pages}, testLimits)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			stub := &stubPDF{texts: tt.texts, openErr: tt.openErr}
			var engine PDFEngine = stub
			if tt.nilEngine {
				engine = nil
			}
			ext, jdg := capsFor(t, tt.extractor), capsFor(t, tt.judge)
			rep, err := Select(t.Context(), doc, ext, jdg, "", tt.images, stubPolicy(tt.mode, engine))
			if got := stub.opens.Load(); got != tt.wantOpens {
				t.Errorf("engine opened %d times, want %d", got, tt.wantOpens)
			}
			switch {
			case tt.wantFail:
				var de *Error
				if err == nil || errors.As(err, &de) {
					t.Fatalf("error = %v, want a non-document error", err)
				}
				return
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), tt.wantMessage) {
					t.Fatalf("error = %v, want %v containing %q", err, tt.wantErr, tt.wantMessage)
				}
				return
			case err != nil:
				t.Fatalf("Select: %v", err)
			}
			if rep.Kind != tt.wantKind || rep.Source != tt.wantSource || !reflect.DeepEqual(rep.MediaTypes, tt.wantTypes) {
				t.Errorf("got %s/%s %v, want %s/%s %v", rep.Kind, rep.Source, rep.MediaTypes, tt.wantKind, tt.wantSource, tt.wantTypes)
			}
			if rep.Groundable != tt.wantGround {
				t.Errorf("Groundable = %v, want %v", rep.Groundable, tt.wantGround)
			}
			if rep.Source == SourceRendered && len(rep.Parts) != len(tt.texts) {
				t.Errorf("%d rendered parts for %d pages", len(rep.Parts), len(tt.texts))
			}
			again, err := Select(t.Context(), doc, ext, jdg, "", tt.images, stubPolicy(tt.mode, engine))
			if err != nil || !reflect.DeepEqual(rep, again) {
				t.Errorf("selecting twice differs (err %v)", err)
			}
		})
	}
}

func TestSelect_pdfTextParts(t *testing.T) {
	t.Parallel()
	doc, err := New(Input{MediaType: "application/pdf", Data: pdfData}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubPDF{texts: []string{"  first page\r\n", "second page "}}
	caps := capsFor(t, []string{"text/plain"})
	rep, err := Select(t.Context(), doc, caps, caps, "", ImagePolicy{}, stubPolicy("text", stub))
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- page 1 ---\nfirst page\n\f\n--- page 2 ---\nsecond page"; rep.Parts[0].Text != want {
		t.Errorf("text part = %q, want %q", rep.Parts[0].Text, want)
	}
	if rep.Label() != "text+text_layer" {
		t.Errorf("Label = %q", rep.Label())
	}
	// Grounding never reads the page markers.
	if strings.Contains(rep.GroundText, "--- page") {
		t.Errorf("GroundText has page markers: %q", rep.GroundText)
	}
}

func TestPDFPolicy(t *testing.T) {
	t.Parallel()
	c := config.PDF{Mode: "auto", DPI: 150, MaxPages: 20, MinTextCharsPerPage: 200, Workers: 4}
	if got := NewPDFPolicy(c, "", nil).String(); got != "auto/dpi=150/min_chars=200" {
		t.Errorf("String = %q", got)
	}
	if got := NewPDFPolicy(c, "native", nil).Mode; got != "native" {
		t.Errorf("application mode = %q, want native", got)
	}
	if PDFEngineOf(nil) != nil {
		t.Error("PDFEngineOf(nil) is not nil")
	}
}

func TestGroundingText(t *testing.T) {
	t.Parallel()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "samples", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	text := capsFor(t, []string{"text/plain", "image/png", "image/jpeg"})
	tests := []struct {
		name      string
		in        Input
		policy    HTMLPolicy
		want      []string // substrings of the grounding text
		notWant   []string
		groundOK  bool
		wantLabel string
	}{
		{name: "txt", in: Input{MediaType: "text/plain", Data: read("booking.txt")},
			want: []string{"HV-48213", "EUR 412.50"}, groundOK: true, wantLabel: "text"},
		{name: "markdown", in: Input{MediaType: "text/markdown", Data: []byte("# Booking HV-1")},
			want: []string{"HV-1"}, groundOK: true, wantLabel: "text"},
		{name: "html raw", in: Input{MediaType: "text/html", Data: read("booking.html")}, policy: RawHTML,
			want: []string{"HV-48213"}, notWant: []string{"<p>", "<b>"}, groundOK: true, wantLabel: "text"},
		{name: "html visible", in: Input{MediaType: "text/html", Data: read("booking.html")}, policy: VisibleText,
			want: []string{"HV-48213"}, notWant: []string{"<p>"}, groundOK: true, wantLabel: "text"},
		{name: "email", in: Input{MediaType: "message/rfc822", Data: read("booking.eml")},
			want: []string{"Subject: Your booking confirmation HV-48213", "HV-48213"}, notWant: []string{"<b>"}, groundOK: true, wantLabel: "text"},
		{name: "image", in: Input{MediaType: "image/png", Data: read("booking.png")}, wantLabel: "native"},
		{name: "pdf as caller pages without engine", in: Input{MediaType: "application/pdf", Data: pdfData, Pages: []Page{{Data: pngData}}},
			wantLabel: "pages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(tt.in, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			rep, err := Select(t.Context(), doc, text, text, tt.policy, ImagePolicy{}, PDFPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Label() != tt.wantLabel {
				t.Errorf("Label = %q, want %q", rep.Label(), tt.wantLabel)
			}
			got, ok, _ := GroundingText(context.Background(), doc, rep, PDFPolicy{})
			if ok != tt.groundOK || rep.Groundable != tt.groundOK {
				t.Fatalf("groundable = %v (rep %v), want %v", ok, rep.Groundable, tt.groundOK)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("grounding text lacks %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("grounding text has %q", w)
				}
			}
		})
	}
}

func TestGroundingText_pdfSentAsGiven(t *testing.T) {
	t.Parallel()
	pdfNative := capsFor(t, []string{"application/pdf", "image/png"})
	vision := capsFor(t, []string{"image/png"})
	for _, tt := range []struct {
		name  string
		caps  []config.Capability
		pages []Page
		texts []string
		want  bool
	}{
		{name: "native, text-rich", caps: pdfNative, texts: []string{richPage}, want: true},
		{name: "caller pages, text-rich", caps: vision, pages: []Page{{Data: pngData}}, texts: []string{richPage}, want: true},
		{name: "native, thin text", caps: pdfNative, texts: []string{thinPage}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New(Input{MediaType: "application/pdf", Data: pdfData, Pages: tt.pages}, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			stub := &stubPDF{texts: tt.texts}
			p := stubPolicy("auto", stub)
			rep, err := Select(t.Context(), doc, tt.caps, tt.caps, "", ImagePolicy{}, p)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Groundable || stub.opens.Load() != 0 {
				t.Fatalf("selection opened the PDF (%d) or made it groundable", stub.opens.Load())
			}
			text, ok, _ := GroundingText(context.Background(), doc, rep, p)
			if ok != tt.want || (ok && !strings.Contains(text, "HV-48213")) {
				t.Errorf("GroundingText = %v, want %v", ok, tt.want)
			}
			if _, ok, _ := GroundingText(context.Background(), doc, rep, PDFPolicy{}); ok {
				t.Error("groundable without an engine")
			}
		})
	}
}

// realEngine is one pdfium engine for the package's tests, with a compilation cache
// shared by test runs.
var realEngine = sync.OnceValues(func() (*pdf.Engine, error) {
	return pdf.New(pdf.Config{CacheDir: filepath.Join(os.TempDir(), "vde-pdfium-test-cache"), MaxPages: 20})
})

func engineForTest(t *testing.T) PDFEngine {
	t.Helper()
	e, err := realEngine()
	if err != nil {
		t.Fatal(err)
	}
	return PDFEngineOf(e)
}

func TestSelect_bookingPDF(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "samples", "booking.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := New(Input{MediaType: "application/pdf", Data: data}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	engine := engineForTest(t)
	textOnly := capsFor(t, []string{"text/plain"})
	vision := capsFor(t, []string{"image/png", "image/jpeg"})
	pdfNative := capsFor(t, []string{"application/pdf"})
	for _, tt := range []struct {
		mode  string
		caps  []config.Capability
		label string
	}{
		{mode: "auto", caps: textOnly, label: "text+text_layer"},
		{mode: "text", caps: textOnly, label: "text+text_layer"},
		{mode: "pages", caps: vision, label: "pages+rendered"},
		{mode: "native", caps: pdfNative, label: "native"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			p := PDFPolicy{Mode: tt.mode, DPI: 150, MaxPages: 20, MinTextChars: 200, Engine: engine}
			rep, err := Select(t.Context(), doc, tt.caps, tt.caps, "", ImagePolicy{}, p)
			if err != nil {
				t.Fatalf("Select: %v", err)
			}
			if rep.Label() != tt.label {
				t.Errorf("Label = %q, want %q", rep.Label(), tt.label)
			}
			text, ok, _ := GroundingText(context.Background(), doc, rep, p)
			if !ok || !strings.Contains(text, "HV-48213") || !strings.Contains(text, "412.50") {
				t.Errorf("GroundingText = %v, %d bytes; want groundable with the booking reference", ok, len(text))
			}
			if rep.Source == SourceRendered {
				cfg, err := png.DecodeConfig(bytes.NewReader(rep.Parts[0].Image.Data))
				if err != nil || cfg.Width != 1240 {
					t.Errorf("rendered page: %v, width %d", err, cfg.Width)
				}
			}
		})
	}
}

func TestSelect_imageOnlyPDF(t *testing.T) {
	t.Parallel()
	doc, err := New(Input{MediaType: "application/pdf", Data: pdftest.Build(pdftest.Page{Image: true})}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	p := PDFPolicy{Mode: "auto", DPI: 72, MaxPages: 20, MinTextChars: 200, Engine: engineForTest(t)}
	textOnly := capsFor(t, []string{"text/plain"})
	if _, err := Select(t.Context(), doc, textOnly, textOnly, "", ImagePolicy{}, p); !errors.Is(err, ErrUnsupportedDocument) {
		t.Errorf("text-only groups: error = %v, want unsupported_document", err)
	}
	vision := capsFor(t, []string{"image/png"})
	rep, err := Select(t.Context(), doc, vision, vision, "", ImagePolicy{}, p)
	if err != nil {
		t.Fatalf("vision groups: %v", err)
	}
	if rep.Label() != "pages+rendered" || rep.Groundable {
		t.Errorf("got %s, groundable %v; want pages+rendered, not groundable", rep.Label(), rep.Groundable)
	}
	img, err := png.Decode(bytes.NewReader(rep.Parts[0].Image.Data))
	if err != nil {
		t.Fatal(err)
	}
	// The page is drawn on white: a corner outside the image stays white.
	if r, g, b, _ := img.At(2, 2).RGBA(); color.Gray16Model.Convert(color.RGBA64{uint16(r), uint16(g), uint16(b), 0xffff}).(color.Gray16).Y < 0xf000 {
		t.Error("page background is not white")
	}
	if _, ok, _ := GroundingText(context.Background(), doc, rep, p); ok {
		t.Error("an image-only PDF is groundable")
	}
}

// TestPDFEngineBusy: an engine with no instance free (pdf.ErrBusy) is neither a refusal
// nor an unreadable PDF: selection and the grounding text report it, so callers retry.
// An unreadable PDF stays a quiet "not groundable".
func TestPDFEngineBusy(t *testing.T) {
	t.Parallel()
	vision := capsFor(t, []string{"image/png"})
	pdfNative := capsFor(t, []string{"application/pdf"})
	doc, err := New(Input{MediaType: "application/pdf", Data: pdfData}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	busy := stubPolicy("auto", &stubPDF{openErr: fmt.Errorf("%w: no instance free", pdf.ErrBusy)})
	_, err = Select(t.Context(), doc, vision, vision, "", ImagePolicy{}, busy)
	if !errors.Is(err, pdf.ErrBusy) || errors.Is(err, ErrUnsupportedDocument) {
		t.Errorf("Select = %v, want pdf.ErrBusy and no refusal", err)
	}
	rep, err := Select(t.Context(), doc, pdfNative, pdfNative, "", ImagePolicy{}, busy)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := GroundingText(t.Context(), doc, rep, busy); ok || !errors.Is(err, pdf.ErrBusy) {
		t.Errorf("GroundingText while busy = %v, %v; want pdf.ErrBusy", ok, err)
	}
	invalid := stubPolicy("auto", &stubPDF{openErr: pdf.ErrInvalid})
	if _, ok, err := GroundingText(t.Context(), doc, rep, invalid); ok || err != nil {
		t.Errorf("GroundingText of an unreadable PDF = %v, %v; want not groundable, no error", ok, err)
	}
}

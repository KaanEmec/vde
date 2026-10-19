package document

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/config"
)

// TestUserDocs parses every file under testdata/user-docs (untracked, operator-supplied
// real documents) and reports metadata only. It runs when VDE_USER_DOCS=1.
func TestUserDocs(t *testing.T) {
	if os.Getenv("VDE_USER_DOCS") == "" {
		t.Skip("set VDE_USER_DOCS=1 to parse testdata/user-docs")
	}
	dir := filepath.Join("..", "..", "testdata", "user-docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no user docs: %v", err)
	}
	types := map[string]string{".eml": "message/rfc822", ".pdf": "application/pdf", ".html": "text/html", ".txt": "text/plain", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg"}
	for _, e := range entries {
		mt, ok := types[strings.ToLower(filepath.Ext(e.Name()))]
		if e.IsDir() || !ok {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			doc, err := New(Input{MediaType: mt, Data: data, Filename: e.Name()}, config.Limits{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if doc.Category == CategoryPDF {
				userPDF(t, doc)
				return
			}
			text := capsFor(t, []string{"text/plain", "image/png", "image/jpeg"})
			if rep, err := Select(t.Context(), doc, text, text, RawHTML, ImagePolicy{}, PDFPolicy{}); err == nil {
				t.Logf("representation=%s groundable=%v ground_text_bytes=%d", rep.Label(), rep.Groundable, len(rep.GroundText))
			}
			if doc.Category != CategoryEmail {
				t.Logf("category=%s media=%s bytes=%d", doc.Category, doc.MediaType, len(doc.Data))
				return
			}
			for _, p := range []HTMLPolicy{RawHTML, VisibleText} {
				text, err := doc.Text(p)
				if err != nil {
					t.Errorf("text(%s): %v", p, err)
					continue
				}
				t.Logf("policy=%s text_bytes=%d", p, len(text))
			}
			for _, a := range doc.Attachments {
				t.Logf("attachment media=%s bytes=%d has_name=%v", a.MediaType, a.Size, a.Filename != "")
			}
		})
	}
}

// userPDF reports a PDF's page count, characters per page, the representation chosen
// under auto for a text-only and an image-only group, grounding and timings. Metadata
// only, never content.
func userPDF(t *testing.T, doc *Document) {
	t.Helper()
	engine := engineForTest(t)
	start := time.Now()
	d, layer, err := readTextLayer(context.Background(), engine, doc.Data, 0)
	if err != nil {
		t.Logf("pdf bytes=%d unreadable: %v", len(doc.Data), err)
		return
	}
	d.Close()
	chars := make([]int, len(layer))
	for i, text := range layer {
		chars[i] = utf8.RuneCountInString(strings.TrimSpace(text))
	}
	t.Logf("pdf bytes=%d pages=%d chars_per_page=%v text_layer=%s", len(doc.Data), len(layer), chars, time.Since(start))
	p := PDFPolicy{Mode: "auto", DPI: 150, MaxPages: 20, MinTextChars: 200, Engine: engine}
	for _, g := range []struct {
		name    string
		accepts []string
	}{{"text-only", []string{"text/plain"}}, {"image-only", []string{"image/png", "image/jpeg"}}} {
		caps := capsFor(t, g.accepts)
		start := time.Now()
		rep, err := Select(t.Context(), doc, caps, caps, "", ImagePolicy{}, p)
		if err != nil {
			t.Logf("group=%s refused: %v (%s)", g.name, err, time.Since(start))
			continue
		}
		t.Logf("group=%s representation=%s parts=%d groundable=%v ground_text_bytes=%d (%s)",
			g.name, rep.Label(), len(rep.Parts), rep.Groundable, len(rep.GroundText), time.Since(start))
	}
}

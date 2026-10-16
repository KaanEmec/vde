package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

package document

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/config"
)

// FuzzNew feeds arbitrary HTML and email bytes through validation and both HTML
// policies: nothing may panic, and accepted text is valid UTF-8.
func FuzzNew(f *testing.F) {
	for _, dir := range []string{"testdata/eml", "testdata/html"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, file := range files {
			if data, err := os.ReadFile(file); err == nil {
				f.Add(data)
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, mt := range []string{"message/rfc822", "text/html"} {
			doc, err := New(Input{MediaType: mt, Data: data}, config.Limits{})
			if err != nil {
				continue
			}
			for _, policy := range []HTMLPolicy{RawHTML, VisibleText} {
				text, err := doc.Text(policy)
				if err != nil || !utf8.ValidString(text) {
					t.Fatalf("%s %s: invalid text, err %v", mt, policy, err)
				}
			}
		}
	})
}

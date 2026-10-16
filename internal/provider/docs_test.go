package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docNames maps a provider package directory to the page that documents it when the two
// differ: the openai package serves the openai_compatible provider.
var docNames = map[string]string{"openai": "openai-compatible"}

// TestEveryProviderIsDocumented keeps the operator documentation honest (contract section
// 10): each adapter package needs a page under docs/providers/, linked from its index.
func TestEveryProviderIsDocumented(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join("..", "..", "docs", "providers")
	index, err := os.ReadFile(filepath.Join(docs, "README.md"))
	if err != nil {
		t.Fatalf("provider index: %v", err)
	}
	packages := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		packages++
		name := e.Name()
		if doc, ok := docNames[name]; ok {
			name = doc
		}
		t.Run(e.Name(), func(t *testing.T) {
			t.Parallel()
			page := name + ".md"
			if _, err := os.Stat(filepath.Join(docs, page)); err != nil {
				t.Errorf("provider package %q has no docs/providers/%s: %v", e.Name(), page, err)
			}
			if !strings.Contains(string(index), "]("+page) {
				t.Errorf("docs/providers/README.md does not link %s", page)
			}
		})
	}
	if packages == 0 {
		t.Fatal("found no provider packages; is the test running in internal/provider?")
	}
}

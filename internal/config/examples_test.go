package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExamplesAreValid loads every examples/*.yaml the way `vde config check` does, with a
// dummy key for every environment variable, and requires that Validate reports no errors.
// Warnings are allowed: the local Ollama example has no PDF reader by design.
func TestExamplesAreValid(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("found %d example configurations, want at least 5", len(files))
	}
	getenv := func(string) string { return "dummy-key-for-tests" }
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse(data, getenv)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			for _, issue := range cfg.Validate() {
				if !issue.Warning {
					t.Errorf("%s", issue)
				}
			}
		})
	}
}

// TestExampleSamples checks that the sample inputs the examples README names exist.
func TestExampleSamples(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"booking.txt", "booking.html", "booking.eml", "booking.pdf", "booking.png",
		"receipt.jpg", "extractor.md", "judge.md", "schema.json",
	} {
		if info, err := os.Stat(filepath.Join("..", "..", "examples", "samples", name)); err != nil || info.Size() == 0 {
			t.Errorf("examples/samples/%s missing or empty: %v", name, err)
		}
	}
}

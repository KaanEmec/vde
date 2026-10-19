package eval_test

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/eval"
	"github.com/kaanemec/vde/internal/extract"
)

const corpusDir = "../../testdata/eval"

// TestCorpus checks the committed corpus: the TravelInbox cases and at least ten
// synthetic cases in two other domains, each expected.json valid against its schema and
// fully correct when compared with itself.
func TestCorpus(t *testing.T) {
	t.Parallel()
	cases, err := eval.LoadCorpus(corpusDir, "*")
	if err != nil {
		t.Fatal(err)
	}
	domains := map[string]int{}
	for _, c := range cases {
		domains[strings.SplitN(c.ID, "-", 2)[0]]++
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			if c.Schema != "" {
				data, err := os.ReadFile(c.Schema)
				if err != nil {
					t.Fatal(err)
				}
				schema, err := extract.CompileSchema(data)
				if err != nil {
					t.Fatalf("schema: %v", err)
				}
				if fb := schema.Validate(c.Expected); len(fb) > 0 {
					t.Errorf("expected.json does not validate: %+v", fb)
				}
			}
			f, err := c.Rules.Compare(c.Expected, c.Expected)
			if err != nil || !f.Correct() || f.Total == 0 {
				t.Errorf("Compare(expected, expected) = %+v, %v; want every field matched", f, err)
			}
		})
	}
	if domains["travelinbox"] != 50 {
		t.Errorf("travelinbox cases = %d, want 50", domains["travelinbox"])
	}
	if domains["receipt"]+domains["lab"] < 10 || domains["receipt"] == 0 || domains["lab"] == 0 {
		t.Errorf("synthetic cases: %d receipt, %d lab; want at least 10 in both domains", domains["receipt"], domains["lab"])
	}
}

// TestParseCasesAndLoadCorpus: a comma-separated list of globs selects the union of
// their cases; an empty or malformed glob is an error.
func TestParseCasesAndLoadCorpus(t *testing.T) {
	t.Parallel()
	patterns, err := eval.ParseCases("receipt-00[12]*, lab-005-*")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := eval.LoadCorpus(corpusDir, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range cases {
		ids = append(ids, c.ID)
	}
	if got := strings.Join(ids, ","); got != "lab-005-thyroid-de,receipt-001-grocery,receipt-002-cafe" {
		t.Errorf("selected %s", got)
	}
	for _, bad := range []string{"", "a,,b", "a,", "[", "a,["} {
		if _, err := eval.ParseCases(bad); err == nil {
			t.Errorf("ParseCases(%q) succeeded", bad)
		}
	}
	if all, err := eval.LoadCorpus(corpusDir); err != nil || len(all) < 60 {
		t.Errorf("LoadCorpus without patterns: %d cases, %v", len(all), err)
	}
}

// TestLoadCorpus_malformed: a case with a file missing in it and in its corpus, two
// documents or an invalid ground truth fails the whole load with the case's id.
func TestLoadCorpus_malformed(t *testing.T) {
	t.Parallel()
	complete := map[string]string{"document.txt": "x", "expected.json": "{}", "extractor.md": "e", "judge.md": "j",
		"compare.yaml": "default: exact\n"}
	for _, tt := range []struct {
		name   string
		change func(files map[string]string)
		want   string
	}{
		{"no document", func(f map[string]string) { delete(f, "document.txt") }, "no document"},
		{"two documents", func(f map[string]string) { f["source.eml"] = "x" }, "more than one"},
		{"no extractor instructions", func(f map[string]string) { delete(f, "extractor.md") }, "no extractor.md"},
		{"no judge instructions", func(f map[string]string) { delete(f, "judge.md") }, "no judge.md"},
		{"no compare rules", func(f map[string]string) { delete(f, "compare.yaml") }, "no compare.yaml"},
		{"invalid ground truth", func(f map[string]string) { f["expected.json"] = "{" }, "not valid JSON"},
		{"invalid truth", func(f map[string]string) { delete(f, "expected.json"); f["truth.json"] = "[" }, "truth.json is not valid JSON"},
		{"bad rules", func(f map[string]string) { f["compare.yaml"] = "default: fuzzy\n" }, "fuzzy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			files := maps.Clone(complete)
			tt.change(files)
			if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
				t.Fatal(err)
			}
			caseDir := filepath.Join(dir, "nested", "c1")
			if err := os.Mkdir(caseDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(caseDir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := eval.LoadCorpus(dir)
			if err == nil || !strings.Contains(err.Error(), "case nested-c1") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadCorpus() = %v, want an error for case nested-c1 naming %q", err, tt.want)
			}
		})
	}
}

// writeCase writes files into dir, creating it.
func writeCase(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLoadCorpus_sharedFiles: a case's own extractor.md, judge.md, schema.json and
// compare.yaml win over the corpus-level ones; a case without them uses the corpus's.
func TestLoadCorpus_sharedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shared := map[string]string{"extractor.md": "e", "judge.md": "j", "schema.json": "{}", "compare.yaml": "default: exact\n"}
	writeCase(t, filepath.Join(dir, "set"), shared)
	writeCase(t, filepath.Join(dir, "set", "own"), map[string]string{"document.txt": "x", "expected.json": "{}",
		"extractor.md": "e", "judge.md": "j", "schema.json": "{}", "compare.yaml": "default: exact\n"})
	writeCase(t, filepath.Join(dir, "set", "shared"), map[string]string{"document.txt": "x", "expected.json": "{}"})
	cases, err := eval.LoadCorpus(dir)
	if err != nil || len(cases) != 2 {
		t.Fatalf("LoadCorpus() = %d cases, %v; want 2", len(cases), err)
	}
	for _, c := range cases {
		wantDir := filepath.Join(dir, "set")
		if c.ID == "set-own" {
			wantDir = filepath.Join(dir, "set", "own")
		}
		for name, got := range map[string]string{"extractor.md": c.ExtractorInstructions, "judge.md": c.JudgeInstructions, "schema.json": c.Schema} {
			if want := filepath.Join(wantDir, name); got != want {
				t.Errorf("%s: %s = %s, want %s", c.ID, name, got, want)
			}
		}
	}
}

// TestLoadCorpus_danglingLink: an entry the --cases globs cannot select is not read, so a
// dangling link beside the selected cases does not fail the load; without globs it does.
func TestLoadCorpus_danglingLink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCase(t, filepath.Join(dir, "set", "c1"), map[string]string{"document.txt": "x", "expected.json": "{}",
		"extractor.md": "e", "judge.md": "j", "compare.yaml": "default: exact\n"})
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "set", "gone")); err != nil {
		t.Fatal(err)
	}
	for _, patterns := range [][]string{{"set-c*"}, {"set-c1"}, {"set-?1", "other-*"}} {
		if cases, err := eval.LoadCorpus(dir, patterns...); err != nil || len(cases) != 1 || cases[0].ID != "set-c1" {
			t.Errorf("LoadCorpus(%q) = %d cases, %v; want set-c1", patterns, len(cases), err)
		}
	}
	if _, err := eval.LoadCorpus(dir); err == nil {
		t.Error("LoadCorpus() of every case succeeded with a dangling link")
	}
}

package eval_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/eval"
	"github.com/kaanemec/vde/internal/extract"
)

const (
	corpusDir      = "../../testdata/eval"
	travelInboxDir = "../../testdata/travelinbox"
)

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

// TestImportTravelInbox converts testdata/travelinbox and checks the result is the
// committed corpus, so the two cannot drift. VDE_EVAL_UPDATE=1 rewrites the committed
// cases instead (the same as vde eval import-travelinbox).
func TestImportTravelInbox(t *testing.T) { // not parallel: with VDE_EVAL_UPDATE it writes the corpus TestCorpus reads
	if os.Getenv("VDE_EVAL_UPDATE") != "" {
		if _, err := eval.ImportTravelInbox(travelInboxDir, corpusDir); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	n, err := eval.ImportTravelInbox(travelInboxDir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 50 {
		t.Errorf("ImportTravelInbox() = %d cases, want 50", n)
	}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		got, _ := os.ReadFile(p)
		want, err := os.ReadFile(filepath.Join(corpusDir, rel))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs from the committed corpus (rerun vde eval import-travelinbox): %v", rel, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportTravelInbox_noCorpus(t *testing.T) {
	t.Parallel()
	if _, err := eval.ImportTravelInbox(t.TempDir(), t.TempDir()); err == nil {
		t.Error("ImportTravelInbox(empty dir) succeeded, want an error")
	}
}

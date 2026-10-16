package eval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TravelInboxPrefix starts the id of every case converted from the TravelInbox corpus.
const TravelInboxPrefix = "travelinbox-"

// TravelInboxCompare is the compare.yaml of every TravelInbox case, after the corpus's
// labelling conventions: title and notes are free text (compared loosely there, not at all
// here), confidence is the extractor's own estimate, texts copied "as printed" are
// compared normalized, the price amount is a decimal string, travellers are a set.
const TravelInboxCompare = `# compare.yaml of the TravelInbox corpus (written by vde eval import-travelinbox).
# Every value of expected.json not listed here is compared exactly.
default: exact
rules:
  - {pointer: /notes, mode: ignore}                       # free text
  - {pointer: /reservations/*/title, mode: ignore}        # free text
  - {pointer: /reservations/*/confidence, mode: ignore}   # the extractor's estimate
  - {pointer: /reservations/*/cancellation, mode: normalized}
  - {pointer: /reservations/*/travellers, mode: set, elements: normalized}
  - {pointer: /reservations/*/price/amount, mode: numeric, tolerance: 0.005}
  - {pointer: /reservations/*/start_place/name, mode: normalized}
  - {pointer: /reservations/*/start_place/address, mode: normalized}
  - {pointer: /reservations/*/end_place/name, mode: normalized}
  - {pointer: /reservations/*/end_place/address, mode: normalized}
  - {pointer: /reservations/*/details/address, mode: normalized}
  - {pointer: /reservations/*/details/room, mode: normalized}
  - {pointer: /reservations/*/details/seat, mode: normalized}
  - {pointer: /trip_hint/destination, mode: normalized}
`

// travelInboxLayout locates the corpus, prompts and schema inside a source directory.
type travelInboxLayout struct{ corpus, extractor, judge, schema string }

// travelInboxLayouts are the two source layouts: this repository's testdata/travelinbox
// and a TravelInbox checkout.
var travelInboxLayouts = []travelInboxLayout{
	{"corpus", "prompts/extractor.md", "prompts/judge.md", "extraction-schema.json"},
	{"backend/testdata/corpus", "backend/internal/extract/prompts/extractor.md",
		"backend/internal/extract/prompts/judge.md", "shared/extraction-schema.json"},
}

// ImportTravelInbox converts the TravelInbox corpus under from (testdata/travelinbox or a
// TravelInbox checkout) into cases travelinbox-<nnn>-<kind> under to and returns how many
// it wrote. Each case gets the document (source.* as document.*), page images when
// present, truth.json as expected.json, notes.md, the shared prompts and schema and
// TravelInboxCompare. Re-running it refreshes the cases; it removes none.
func ImportTravelInbox(from, to string) (int, error) {
	var layout *travelInboxLayout
	for i, l := range travelInboxLayouts {
		if fi, err := os.Stat(filepath.Join(from, l.corpus)); err == nil && fi.IsDir() {
			layout = &travelInboxLayouts[i]
			break
		}
	}
	if layout == nil {
		return 0, fmt.Errorf("%s: no corpus/ or backend/testdata/corpus/ directory", from)
	}
	shared := map[string]string{
		FileExtractor: filepath.Join(from, layout.extractor),
		FileJudge:     filepath.Join(from, layout.judge),
		FileSchema:    filepath.Join(from, layout.schema),
	}
	sharedData := make(map[string][]byte, len(shared))
	for name, p := range shared {
		b, err := os.ReadFile(p)
		if err != nil {
			return 0, err
		}
		sharedData[name] = b
	}

	src := filepath.Join(from, layout.corpus)
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(src, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "truth.json")); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := importTravelInboxCase(dir, filepath.Join(to, TravelInboxPrefix+e.Name()), sharedData); err != nil {
			return n, fmt.Errorf("%s: %w", e.Name(), err)
		}
		n++
	}
	return n, nil
}

func importTravelInboxCase(src, dst string, shared map[string][]byte) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	// A refreshed case may have changed its document type or pages.
	old, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, e := range old {
		if name := e.Name(); strings.HasPrefix(name, "document.") || strings.HasPrefix(name, "page-") {
			if err := os.Remove(filepath.Join(dst, name)); err != nil {
				return err
			}
		}
	}

	files := map[string][]byte{FileCompare: []byte(TravelInboxCompare)}
	for name, b := range shared {
		files[name] = b
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	documents := 0
	for _, e := range entries {
		name := e.Name()
		var target string
		switch {
		case e.IsDir():
			continue
		case strings.HasPrefix(name, "source."):
			target = "document" + strings.ToLower(filepath.Ext(name))
			documents++
		case strings.HasPrefix(name, "page-"):
			target = name
		case name == "truth.json":
			target = FileExpected
		case name == "notes.md":
			target = name
		default:
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return err
		}
		files[target] = b
	}
	if documents != 1 {
		return fmt.Errorf("%d source.* files, want 1", documents)
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

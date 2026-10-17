package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Case file names (see the package documentation).
const (
	FileExtractor = "extractor.md"
	FileJudge     = "judge.md"
	FileSchema    = "schema.json"
	FileExpected  = "expected.json"
	FileCompare   = "compare.yaml"
)

// Case is one corpus case. Paths are file paths; Expected and Rules are loaded.
type Case struct {
	ID                    string
	Dir                   string
	Document              string   // the document.* file
	Pages                 []string // page-* images in name order
	ExtractorInstructions string
	JudgeInstructions     string
	Schema                string // "" for schema-less
	Expected              json.RawMessage
	Rules                 Rules
}

// ParseCases splits a comma-separated list of case id globs (path.Match syntax, as
// --cases takes it: "ti-0*,receipt-*"). Spaces around a glob are dropped; an empty glob
// or a malformed one is an error.
func ParseCases(list string) ([]string, error) {
	var patterns []string
	for p := range strings.SplitSeq(list, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("cases %q: empty glob", list)
		}
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("cases %q: %w", p, err)
		}
		patterns = append(patterns, p)
	}
	return patterns, nil
}

// LoadCorpus loads the cases of dir whose id matches any of patterns (path.Match syntax;
// none, "" or "*" for all), sorted by id. Subdirectories without expected.json are not
// cases and are skipped; a case with expected.json and a missing or malformed file is an
// error.
func LoadCorpus(dir string, patterns ...string) ([]Case, error) {
	for _, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("cases %q: %w", p, err)
		}
	}
	selected := func(id string) bool {
		if len(patterns) == 0 {
			return true
		}
		for _, p := range patterns {
			if ok, _ := path.Match(p, id); ok || p == "" {
				return true
			}
		}
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() || !selected(e.Name()) {
			continue
		}
		caseDir := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(caseDir, FileExpected)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		c, err := LoadCase(caseDir)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	slices.SortFunc(cases, func(a, b Case) int { return strings.Compare(a.ID, b.ID) })
	return cases, nil
}

// LoadCase loads the case in dir.
func LoadCase(dir string) (Case, error) {
	c := Case{ID: filepath.Base(dir), Dir: dir}
	fail := func(err error) (Case, error) { return Case{}, fmt.Errorf("case %s: %w", c.ID, err) }

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fail(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		switch {
		case strings.HasPrefix(name, "document."):
			if c.Document != "" {
				return fail(errors.New("more than one document.* file"))
			}
			c.Document = filepath.Join(dir, name)
		case strings.HasPrefix(name, "page-"):
			c.Pages = append(c.Pages, filepath.Join(dir, name)) // ReadDir sorts by name
		}
	}
	if c.Document == "" {
		return fail(errors.New("no document.* file"))
	}
	for _, req := range []struct {
		name string
		dst  *string
	}{{FileExtractor, &c.ExtractorInstructions}, {FileJudge, &c.JudgeInstructions}} {
		p := filepath.Join(dir, req.name)
		if _, err := os.Stat(p); err != nil {
			return fail(err)
		}
		*req.dst = p
	}
	if _, err := os.Stat(filepath.Join(dir, FileSchema)); err == nil {
		c.Schema = filepath.Join(dir, FileSchema)
	}

	expected, err := os.ReadFile(filepath.Join(dir, FileExpected))
	if err != nil {
		return fail(err)
	}
	if !json.Valid(expected) {
		return fail(fmt.Errorf("%s is not valid JSON", FileExpected))
	}
	c.Expected = expected
	rules, err := os.ReadFile(filepath.Join(dir, FileCompare))
	if err != nil {
		return fail(err)
	}
	if c.Rules, err = ParseRules(rules); err != nil {
		return fail(err)
	}
	return c, nil
}

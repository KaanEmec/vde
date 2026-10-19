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
	FileTruth     = "truth.json" // the TravelInbox layout's expected.json
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
// none, "" or "*" for all), sorted by id. A subdirectory with expected.json or truth.json
// is a case; one without is a nested corpus (one level, a symbolic link allowed) whose
// case ids are prefixed with its name and "-". A case with a missing or malformed file is
// an error.
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
	cases, err := loadCorpusDir(dir, "", true, selected)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(cases, func(a, b Case) int { return strings.Compare(a.ID, b.ID) })
	return cases, nil
}

// loadCorpusDir loads the selected cases of dir with ids prefix+name, and those of its
// nested corpora when nested is set.
func loadCorpusDir(dir, prefix string, nested bool, selected func(string) bool) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, e := range entries {
		sub := filepath.Join(dir, e.Name())
		fi, err := os.Stat(sub) // follows a symbolic link
		if err != nil {
			return nil, err
		}
		switch {
		case !fi.IsDir():
		case fileOf(FileExpected, sub) != "" || fileOf(FileTruth, sub) != "":
			if id := prefix + e.Name(); selected(id) {
				c, err := loadCase(sub, id, dir)
				if err != nil {
					return nil, err
				}
				cases = append(cases, c)
			}
		case nested:
			more, err := loadCorpusDir(sub, prefix+e.Name()+"-", false, selected)
			if err != nil {
				return nil, err
			}
			cases = append(cases, more...)
		}
	}
	return cases, nil
}

// fileOf returns the path of name in the first of dirs that has it, "" when none does.
func fileOf(name string, dirs ...string) string {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// loadCase loads the case in dir with the given id; extractor.md, judge.md, schema.json
// and compare.yaml it lacks are read from corpus, the directory that holds it.
func loadCase(dir, id, corpus string) (Case, error) {
	c := Case{ID: id, Dir: dir}
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
		case strings.HasPrefix(name, "document."), strings.HasPrefix(name, "source."):
			if c.Document != "" {
				return fail(errors.New("more than one document.* or source.* file"))
			}
			c.Document = filepath.Join(dir, name)
		case strings.HasPrefix(name, "page-"):
			c.Pages = append(c.Pages, filepath.Join(dir, name)) // ReadDir sorts by name
		}
	}
	if c.Document == "" {
		return fail(errors.New("no document.* file"))
	}
	if c.ExtractorInstructions = fileOf(FileExtractor, dir, corpus); c.ExtractorInstructions == "" {
		return fail(errors.New("no " + FileExtractor))
	}
	if c.JudgeInstructions = fileOf(FileJudge, dir, corpus); c.JudgeInstructions == "" {
		return fail(errors.New("no " + FileJudge))
	}
	c.Schema = fileOf(FileSchema, dir, corpus)

	expectedPath := fileOf(FileExpected, dir)
	if expectedPath == "" {
		expectedPath = filepath.Join(dir, FileTruth)
	}
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		return fail(err)
	}
	if !json.Valid(expected) {
		return fail(fmt.Errorf("%s is not valid JSON", filepath.Base(expectedPath)))
	}
	c.Expected = expected
	comparePath := fileOf(FileCompare, dir, corpus)
	if comparePath == "" {
		return fail(errors.New("no " + FileCompare))
	}
	rules, err := os.ReadFile(comparePath)
	if err != nil {
		return fail(err)
	}
	if c.Rules, err = ParseRules(rules); err != nil {
		return fail(err)
	}
	return c, nil
}

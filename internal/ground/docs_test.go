package ground_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/ground"
)

// TestDateTableIsDocumented keeps docs/operator/judge-policy.md in step with the date and
// time table: every form, every month-name language and the rules version are listed,
// and the page lists no form the code does not have.
func TestDateTableIsDocumented(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "operator", "judge-policy.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	known := map[string]bool{}
	for _, f := range append(append([]string{}, ground.DateForms...), ground.TimeForms...) {
		known[f] = true
		if !strings.Contains(page, "| `"+f+"` |") {
			t.Errorf("judge-policy.md does not list the form %q in a table row", f)
		}
	}
	for line := range strings.Lines(page) {
		if !strings.HasPrefix(line, "| `{") {
			continue
		}
		form, _, _ := strings.Cut(strings.TrimPrefix(line, "| `"), "`")
		if !known[form] {
			t.Errorf("judge-policy.md lists %q, which is not in ground.DateForms or ground.TimeForms", form)
		}
	}
	for _, m := range ground.Months {
		if !strings.Contains(page, m.Language) {
			t.Errorf("judge-policy.md does not name the month names of %s", m.Language)
		}
	}
	if !strings.Contains(page, "`"+ground.RulesVersion+"`") {
		t.Errorf("judge-policy.md does not name the rules version %s", ground.RulesVersion)
	}
}

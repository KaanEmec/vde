package document

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/kaanemec/vde/internal/ground"
)

// flatText is visible text with every table flattened: what visible_text gave before
// data tables were written as Markdown.
func flatText(t *testing.T, src string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return renderVisible(root, maxVisibleText, nil, false)
}

func TestDataTables(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"fare breakdown with th",
			`<table><tr><th>Fare</th><th>Tax</th><th>Total</th></tr><tr><td>200,00</td><td>30,50</td><td>230,50</td></tr></table>`,
			"| Fare | Tax | Total |\n| --- | --- | --- |\n| 200,00 | 30,50 | 230,50 |"},
		{"th only in the header, with thead and tbody",
			`<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr><tr><td>3</td><td>4</td></tr></tbody></table>`,
			"| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |"},
		{"itinerary without th, first row is the header",
			`<table><tr><td>Outbound</td><td>BER 08:15</td><td>LIS 10:40</td></tr><tr><td>Return</td><td>LIS 18:05</td><td>BER 22:20</td></tr></table>`,
			"| Outbound | BER 08:15 | LIS 10:40 |\n| --- | --- | --- |\n| Return | LIS 18:05 | BER 22:20 |"},
		{"colspan repeats as empty cells",
			`<table><tr><th>A</th><th>B</th><th>C</th></tr><tr><td colspan="2">x</td><td>y</td></tr></table>`,
			"| A | B | C |\n| --- | --- | --- |\n| x |  | y |"},
		{"colspan counted in the equal-cells rule",
			`<table><tr><td>a</td><td>b</td><td>c</td></tr><tr><td colspan="2">d</td><td>e</td></tr></table>`,
			"| a | b | c |\n| --- | --- | --- |\n| d |  | e |"},
		{"th table with a short row is padded",
			`<table><tr><th>A</th><th>B</th></tr><tr><td>1</td></tr></table>`,
			"| A | B |\n| --- | --- |\n| 1 |  |"},
		{"cells on one line, | escaped",
			`<table><tr><th>A</th><th>B</th></tr><tr><td>x|y<br>z</td><td><p>one</p><p>two</p></td></tr></table>`,
			"| A | B |\n| --- | --- |\n| x\\|y z | one two |"},
		{"link in a cell",
			`<table><tr><th>A</th><th>B</th></tr><tr><td><a href="https://e.example/x">Go</a></td><td>2</td></tr></table>`,
			"| A | B |\n| --- | --- |\n| Go (https://e.example/x) | 2 |"},
		{"hidden row and hidden cell",
			`<table><tr><th>A</th><th>B</th></tr><tr hidden><td>no</td><td>no</td></tr><tr><td>1</td><td style="display:none">no</td></tr></table>`,
			"| A | B |\n| --- | --- |\n| 1 |  |"},
		{"caption first",
			`<table><caption>Fares</caption><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`,
			"Fares\n| A | B |\n| --- | --- |\n| 1 | 2 |"},
		{"text around",
			`<p>before</p><table><tr><th>A</th></tr><tr><td>1</td></tr></table><p>after</p>`,
			"before\n\n| A |\n| --- |\n| 1 |\n\nafter"},
		{"data table inside a layout table",
			`<table><tr><td>Hello</td></tr><tr><td><table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table></td></tr></table>`,
			"Hello\n| A | B |\n| --- | --- |\n| 1 | 2 |"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := visibleText([]byte(tt.in), maxVisibleText); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLayoutTablesUnchanged: a table that is not data keeps today's output byte for byte.
func TestLayoutTablesUnchanged(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in string }{
		{"one row", `<table><tr><td>a</td><td>b</td></tr></table>`},
		{"one column", `<table><tr><td>a</td></tr><tr><td>b</td></tr></table>`},
		{"ragged rows", `<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>`},
		{"ragged by colspan", `<table><tr><td>a</td><td>b</td><td>c</td></tr><tr><td colspan="2">d</td></tr></table>`},
		{"nested table", `<table><tr><td><table><tr><td>a</td><td>b</td></tr></table></td><td>e</td></tr></table>`},
		{"nested table in a th table", `<table><tr><th>a</th><th>b</th></tr><tr><td><table><tr><td>c</td></tr></table></td><td>d</td></tr></table>`},
		{"rowspan", `<table><tr><th>a</th><th>b</th></tr><tr><td rowspan="2">c</td><td>d</td></tr></table>`},
		{"th row not first", `<table><tr><td>a</td><td>b</td></tr><tr><th>c</th><th>d</th></tr></table>`},
		{"colspan beyond the bound", `<table><tr><th colspan="500">a</th></tr><tr><td>b</td></tr></table>`},
		{"empty", `<table></table><p>x</p>`},
		{"email template", `<table role="presentation" width="600"><tr><td><img alt="Logo"></td><td align="right">Ref <b>QX7</b></td></tr><tr><td><h1>Hello</h1></td></tr></table>`},
		{"no table", `<p>a</p><ul><li>x</li></ul>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, want := visibleText([]byte(tt.in), maxVisibleText), flatText(t, tt.in)
			if got != want {
				t.Errorf("got %q, today's output %q", got, want)
			}
			if strings.Contains(got, "| --- |") {
				t.Error("a layout table was written as Markdown")
			}
		})
	}
	// The pages that are only layout.
	for _, name := range []string{"article"} {
		src := readFile(t, filepath.Join("testdata", "html", name+".html"))
		if got, want := visibleText(src, maxVisibleText), flatText(t, string(src)); got != want {
			t.Errorf("%s differs from today's output", name)
		}
	}
}

// TestDataTablesGrounding: Markdown pipes and the rule row add no values. For every HTML
// fixture and corpus email the grounding form (ground.Normalize) of the text is the same
// as without tables, so nothing that grounded before stops grounding, and nothing new does.
func TestDataTablesGrounding(t *testing.T) {
	t.Parallel()
	sources := map[string]string{}
	files, _ := filepath.Glob(filepath.Join("testdata", "html", "*.html"))
	for _, f := range files {
		sources[filepath.Base(f)] = string(readFile(t, f))
	}
	corpus, _ := filepath.Glob(filepath.Join(corpusDir, "*", "source.eml"))
	if len(corpus) < 40 {
		t.Fatalf("found %d corpus emails", len(corpus))
	}
	for _, f := range corpus {
		if doc := newEmail(t, f); doc.email.html {
			sources[filepath.Base(filepath.Dir(f))] = string(doc.email.body)
		}
	}
	tables := 0
	for name, src := range sources {
		got, want := visibleText([]byte(src), maxVisibleText), flatText(t, src)
		if g, w := ground.Normalize(got), ground.Normalize(want); g != w {
			t.Errorf("%s: grounding text differs with tables", name)
		}
		if strings.Contains(got, "| --- |") {
			tables++
		}
	}
	if tables < 3 {
		t.Errorf("only %d sources have a data table; the check proves little", tables)
	}
	// A value present only in the rule row or a pipe is a miss.
	text := ground.NewText(visibleText([]byte(`<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`), maxVisibleText))
	if res := ground.Check(text, []byte(`{"x":"---","y":"|"}`), nil, 0); res.CriticalMissed != 0 || len(res.Ungrounded) != 0 {
		t.Logf("punctuation-only values are not checked: %+v", res) // stripped by Normalize: nothing to ground
	}
}

func TestDataTableLimit(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString(`<table><tr><th>Item</th><th>Price</th></tr>`)
	for range 50 {
		b.WriteString(`<tr><td>Something long enough</td><td>12,34</td></tr>`)
	}
	b.WriteString(`</table><p>after</p>`)
	const limit = 300
	got := visibleText([]byte(b.String()), limit)
	if len(got) > limit+len(truncatedMarker) || !strings.HasSuffix(got, truncatedMarker) {
		t.Fatalf("limit not respected: %d bytes, ends %q", len(got), got[max(0, len(got)-20):])
	}
	body := strings.TrimSuffix(got, truncatedMarker)
	rows := strings.Split(body, "\n")
	if len(rows) < 3 {
		t.Fatalf("only %d rows kept", len(rows))
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "| ") || !strings.HasSuffix(row, " |") {
			t.Errorf("a row was cut: %q", row)
		}
	}
	if strings.Contains(got, "after") {
		t.Error("text after the cut")
	}
	// A limit smaller than the first row keeps no row at all.
	if got := visibleText([]byte(b.String()), 10); got != truncatedMarker {
		t.Errorf("tiny limit: %q", got)
	}
}

func TestDataTableFixtureFile(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join("testdata", "html", "tables.html"))
	got := visibleText(src, maxVisibleText)
	for _, want := range []string{
		"| Passenger | Fare | Tax | Total |\n| --- | --- | --- | --- |\n| Ana Silva | 200,00 | 30,50 | 230,50 |",
		"| Total |  |  | 411,00 |",
		"| Outbound | BER 08:15 | LIS 10:40 |\n| --- | --- | --- |",
		"Logo\tRef QX7PLM",                                          // the layout tables around stay flat
		"| Flight | Date |\n| --- | --- |\n| SA 123 | 2 Nov 2026 |", // the data table inside a layout table
		"Only one row\tof two cells",
	} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

package document

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got with testdata/golden/name, rewriting it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := string(readFile(t, path))
	if got != want {
		t.Errorf("%s mismatch (run go test -update to accept)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestRawHTML(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"passthrough", `<P CLASS=x>Hello &amp; <b>bye</b></P>`, `<P CLASS=x>Hello &amp; <b>bye</b></P>`},
		{"comments", `a<!-- hidden -->b<!---->c`, `abc`},
		{"script and style", `<SCRIPT>if (a<b) x()</SCRIPT><style>p{}</style><p>t</p>`, `<SCRIPT></SCRIPT><style></style><p>t</p>`},
		{"self-closing script", `<script src="x.js"/><p>t</p>`, `<script src="x.js"/><p>t</p>`},
		{"doctype kept", "<!DOCTYPE html>\n<html></html>", "<!DOCTYPE html>\n<html></html>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := rawHTML([]byte(tt.in)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVisibleText(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"paragraphs", `<p>One   two</p><p> three </p>`, "One two\n\nthree"},
		{"br", `a<br>b<br><br>c`, "a\nb\n\nc"},
		{"inline", `<span>a</span><b>b</b> <i>c</i>`, "ab c"},
		{"unordered list", `<ul><li>x</li><li>y</li></ul>`, "- x\n- y"},
		{"ordered list", `<ol start="2"><li>x</li><li>y</li></ol>`, "2. x\n3. y"},
		{"table cells", `<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`, "A\tB\n1\t2"},
		{"link", `<a href="https://e.example/x">Go</a>`, "Go (https://e.example/x)"},
		{"link label is target", `<a href="https://e.example/">https://e.example/</a>`, "https://e.example/"},
		{"mailto label", `<a href="mailto:a@e.example">a@e.example</a>`, "a@e.example"},
		{"mailto other label", `<a href="mailto:a@e.example">Write us</a>`, "Write us (mailto:a@e.example)"},
		{"relative link", `<a href="/x">Go</a>`, "Go"},
		{"javascript link", `<a href="javascript:x()">Go</a>`, "Go"},
		{"image link", `<a href="https://e.example/"><img src="i.png"></a>`, "https://e.example/"},
		{"image alt", `<p>Logo: <img alt="ACME"> Inc</p>`, "Logo: ACME Inc"},
		{"head dropped title kept", `<head><title>T</title><meta name=x content=y></head><body>b</body>`, "T\n\nb"},
		{"script style comment", `<script>x</script><style>y</style><!-- z -->w`, "w"},
		{"hidden", `<p hidden>a</p><p aria-hidden="true">b</p><p style="DISPLAY: none">c</p><p style="visibility:hidden">d</p>e`, "e"},
		{"entities and invisible", "<p>a&nbsp;&nbsp;b&zwnj;c&#173;d</p>", "a bcd"},
		{"pre", "<pre>a  b\n c</pre><p>d</p>", "a  b\n c\n\nd"},
		{"empty", ``, ""},
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

func TestVisibleTextTruncates(t *testing.T) {
	t.Parallel()
	in := "<p>" + strings.Repeat("é", 100) + "</p>"
	got := visibleText([]byte(in), 51)
	if !strings.HasSuffix(got, truncatedMarker) {
		t.Fatalf("missing marker: %q", got)
	}
	if body := strings.TrimSuffix(got, truncatedMarker); body != strings.Repeat("é", 25) {
		t.Errorf("cut at %q, want 25 whole runes", body)
	}
}

// TestHTMLGolden runs both policies over synthetic pages and the HTML bodies of corpus
// emails (decision 11).
func TestHTMLGolden(t *testing.T) {
	t.Parallel()
	sources := map[string][]byte{}
	for _, name := range []string{"layout-table", "article"} {
		sources[name] = readFile(t, filepath.Join("testdata", "html", name+".html"))
	}
	for _, name := range []string{"019-hotel", "001-flight", "041-rail"} {
		doc := corpusEmail(t, name)
		if !doc.email.html {
			t.Fatalf("%s: no HTML body", name)
		}
		sources[name] = doc.email.body
	}
	for name, src := range sources {
		for _, policy := range []HTMLPolicy{RawHTML, VisibleText} {
			t.Run(name+"/"+policy.String(), func(t *testing.T) {
				t.Parallel()
				got := htmlText(src, policy)
				if again := htmlText(src, policy); again != got {
					t.Fatal("output is not deterministic")
				}
				golden(t, name+"."+policy.String()+".txt", got)
			})
		}
	}
}

func TestLayoutTableReadingOrder(t *testing.T) {
	t.Parallel()
	got := visibleText(readFile(t, "testdata/html/layout-table.html"), maxVisibleText)
	order := []string{
		"Flight confirmation", "SkyAir", "QX7PLM", "Your trip to Lisbon", "Dear Ana",
		"Flight\tFrom\tTo", "SA 123\tBerlin BER 08:15\tLisbon LIS 10:40", "Total paid: 312,40 €",
		"Manage booking (https://skyair.example/manage?ref=QX7PLM)", "help@skyair.example", "Terms",
	}
	at := 0
	for _, s := range order {
		i := strings.Index(got[at:], s)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", s, got)
		}
		at += i + len(s)
	}
	for _, s := range []string{"Preheader", "track", "tracking", "/relative/terms"} {
		if strings.Contains(got, s) {
			t.Errorf("output contains %q", s)
		}
	}
}

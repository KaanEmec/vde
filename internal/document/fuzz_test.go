package document

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/kaanemec/vde/internal/ground"
)

// FuzzNew feeds arbitrary HTML and email bytes through validation and both HTML
// policies: nothing may panic, and accepted text is valid UTF-8.
func FuzzNew(f *testing.F) {
	for _, dir := range []string{"testdata/eml", "testdata/html", "testdata/sections"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, file := range files {
			if data, err := os.ReadFile(file); err == nil {
				f.Add(data)
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, mt := range []string{"message/rfc822", "text/html"} {
			doc, err := New(Input{MediaType: mt, Data: data}, testLimits)
			if err != nil {
				continue
			}
			for _, policy := range []HTMLPolicy{RawHTML, VisibleText} {
				text, err := doc.Text(policy)
				if err != nil || !utf8.ValidString(text) {
					t.Fatalf("%s %s: invalid text, err %v", mt, policy, err)
				}
				if doc.email != nil {
					checkLabelled(t, text, doc.email.labelledText(policy))
				}
			}
		}
	})
}

// checkLabelled: labels only add lines, so without them the text is what it was, and it
// stays valid UTF-8. A text that has a label line of its own is not checked.
func checkLabelled(t *testing.T, plain, labelled string) {
	t.Helper()
	if !utf8.ValidString(labelled) {
		t.Fatal("labelled text is not valid UTF-8")
	}
	if len(labelLines(plain)) == 0 && stripLabels(labelled) != plain {
		t.Fatal("text without labels differs from the unlabelled text")
	}
}

// FuzzSectionLabels: labelling plain text and HTML adds label lines and nothing else.
func FuzzSectionLabels(f *testing.F) {
	for _, s := range []string{
		"Hi\n> quoted\n> more\nBye",
		"On Fri, Ben wrote:\r\n> x\r\n",
		"-----Original Message-----\nFrom: A\nSent: x\nTo: B\nSubject: s\n\ntext\n",
		"Begin forwarded message:\n\n> From: A\n> text\n",
		`<blockquote type="cite">a<div class="gmail_quote">---------- Forwarded message ---------<p>x</p></div></blockquote>`,
		`<p>-----Original Message-----</p><p>From: A Sent: B To: C Subject: D</p>`,
	} {
		f.Add([]byte(s))
	}
	files, _ := filepath.Glob("testdata/sections/*")
	for _, file := range files {
		if data, err := os.ReadFile(file); err == nil {
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if !utf8.Valid(src) {
			return
		}
		checkLabelled(t, string(src), labelPlain(string(src)))
		checkLabelled(t, visibleText(src, maxVisibleText), visibleTextLabelled(src, maxVisibleText))
	})
}

// FuzzHTMLText feeds arbitrary HTML to both HTML-to-text paths: nothing may panic,
// raw_html never grows the input, visible_text stays within its cap (here a small one,
// so truncation is reached) and turns valid UTF-8 into valid UTF-8.
func FuzzHTMLText(f *testing.F) {
	files, _ := filepath.Glob("testdata/html/*")
	for _, file := range files {
		if data, err := os.ReadFile(file); err == nil {
			f.Add(data)
		}
	}
	for _, s := range []string{
		`<p>Flight <b>LH 123</b><br>Gate&nbsp;B12</p><script>x()</script><style>p{}</style>`,
		`<table><tr><td>a</td><td>b</td></tr></table><ul><li>one<li>two</ul><ol start="3"><li>x</ol>`,
		`<a href="https://example.test/b?x=1">Book</a> <a href="javascript:alert(1)">x</a> <img alt="logo">`,
		`<pre>  keep   spaces
	and tabs</pre><div hidden>no</div><span style="display:none">no</span>`,
		"<p>\u200bzero\u00adwidth\ufeff</p><!-- comment --><title>T</title>",
		`<div><div><div><p>unclosed`,
		"\xff\xfe<p>not UTF-8</p>",
	} {
		f.Add([]byte(s))
	}
	const limit = 256
	f.Fuzz(func(t *testing.T, src []byte) {
		if raw := rawHTML(src); len(raw) > len(src) {
			t.Fatalf("raw_html grew %d bytes to %d", len(src), len(raw))
		}
		text := visibleText(src, limit)
		if len(text) > limit+len(truncatedMarker) {
			t.Fatalf("visible_text is %d bytes, cap %d", len(text), limit)
		}
		if utf8.Valid(src) && !utf8.ValidString(text) {
			t.Fatal("visible_text of valid UTF-8 is not valid UTF-8")
		}
		// Data tables change only whitespace and pipes: grounding reads the same words.
		if utf8.Valid(src) && !bytes.Contains(src, []byte("|")) {
			root, _ := html.Parse(bytes.NewReader(src))
			if got, want := ground.Normalize(visibleText(src, maxVisibleText)), ground.Normalize(renderVisible(root, maxVisibleText, nil, false)); got != want {
				t.Fatal("data tables changed the grounding text")
			}
		}
	})
}

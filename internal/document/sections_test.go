package document

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/ground"
)

var sectionLabels = []string{
	sectionForwarded.startLabel(), sectionForwarded.endLabel(),
	sectionQuoted.startLabel(), sectionQuoted.endLabel(),
}

// labelLines returns the label lines of text in order.
func labelLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if t := strings.TrimSuffix(l, "\r"); slicesContains(sectionLabels, t) {
			out = append(out, t)
		}
	}
	return out
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// stripLabels removes the label lines: what is left must be the text without labels.
func stripLabels(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0:0]
	for _, l := range lines {
		if !slicesContains(sectionLabels, strings.TrimSuffix(l, "\r")) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

const (
	fwdStart, fwdEnd = "[forwarded message]", "[end of forwarded message]"
	qStart, qEnd     = "[quoted earlier reply]", "[end of quoted earlier reply]"
)

func TestEmailSectionLabels(t *testing.T) {
	t.Parallel()
	textOnly := []string{"text/plain"}
	tests := []struct {
		file string
		// visible is the label sequence under visible_text, raw the one under raw_html
		// (labels are not inserted into HTML source).
		visible, raw []string
		// inside names a line that must lie between the first start label and the last
		// end label, so the section covers its content; outside one that must not.
		inside, outside string
	}{
		{file: "gmail-forward.eml", visible: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "please keep this"},
		{file: "outlook-forward.eml", visible: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "Thanks, Ana"},
		{file: "outlook-original-message.eml", visible: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "Forwarding the confirmation"},
		{file: "apple-forward.eml", visible: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "Booking for you"},
		{file: "apple-forward-ios.eml", visible: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "Booking for you"},
		{file: "apple-reply.eml", visible: []string{qStart, qEnd}, inside: "Your flight is at 08:15", outside: "Thanks, that works"},
		{file: "gmail-reply.eml", visible: []string{qStart, qEnd}, inside: "Your flight is at 08:15", outside: "Thanks, that works"},
		{file: "forward-in-reply.eml", visible: []string{qStart, fwdStart, fwdEnd, qEnd}, inside: "Hotel Aurora, 3 nights", outside: "Thanks, got it"},
		{file: "html-none.eml", visible: nil},
		{file: "plain-quote.eml", visible: []string{qStart, qEnd}, raw: []string{qStart, qEnd}, inside: "Gate B12", outside: "Thanks, that works"},
		{file: "plain-forward-apple.eml", visible: []string{fwdStart, fwdEnd}, raw: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "Booking for you"},
		{file: "plain-outlook.eml", visible: []string{fwdStart, fwdEnd}, raw: []string{fwdStart, fwdEnd}, inside: "Flight SA 123", outside: "Please see below"},
		{file: "plain-none.eml", visible: nil},
	}
	for _, tt := range tests {
		for _, policy := range []HTMLPolicy{VisibleText, RawHTML} {
			t.Run(tt.file+"/"+policy.String(), func(t *testing.T) {
				t.Parallel()
				doc := newEmail(t, "testdata/sections/"+tt.file)
				want := tt.visible
				if policy == RawHTML {
					want = tt.raw
					if !strings.HasPrefix(tt.file, "plain") {
						want = nil
					}
				}
				rep, err := Select(t.Context(), doc, capsFor(t, textOnly), capsFor(t, textOnly), policy, ImagePolicy{}, PDFPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				model := rep.Parts[0].Text
				if got := labelLines(model); !reflect.DeepEqual(got, want) {
					t.Fatalf("labels = %v, want %v", got, want)
				}
				// Nothing is removed: without the label lines the text is today's output.
				today, err := doc.Text(policy)
				if err != nil {
					t.Fatal(err)
				}
				if got := stripLabels(model); got != withContext(doc.contextLine(0), today) {
					t.Errorf("text without labels differs from the unlabelled text:\n%q\n%q", got, today)
				}
				if rep.GroundText != mustText(t, doc, VisibleText, rep) || len(labelLines(rep.GroundText)) != 0 {
					t.Error("grounding text has labels or differs from the unlabelled text")
				}
				if len(want) == 0 {
					return
				}
				first := strings.Index(model, want[0])
				last := strings.LastIndex(model, want[len(want)-1])
				if i := strings.Index(model, tt.inside); i < first || i > last {
					t.Errorf("%q is not inside the labelled section", tt.inside)
				}
				if i := strings.Index(model, tt.outside); i > first {
					t.Errorf("%q is inside the labelled section", tt.outside)
				}
			})
		}
	}
}

// mustText is the grounding text of an email: its text without labels, under visible_text
// for HTML bodies and as it is for plain ones.
func mustText(t *testing.T, doc *Document, policy HTMLPolicy, rep Representation) string {
	t.Helper()
	text, err := doc.Text(policy)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// TestSectionLabelsNotGrounding: a label carries no value, and the grounding text has no
// labels, so a candidate value found only in a label is a miss.
func TestSectionLabelsNotGrounding(t *testing.T) {
	t.Parallel()
	tests := []struct{ file, phrase, label string }{
		{"outlook-forward.eml", "forwarded message", fwdStart},
		{"apple-reply.eml", "quoted earlier reply", qStart},
	}
	textOnly := capsFor(t, []string{"text/plain"})
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			rep, err := Select(t.Context(), newEmail(t, "testdata/sections/"+tt.file), textOnly, textOnly, VisibleText, ImagePolicy{}, PDFPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rep.Parts[0].Text, tt.label) {
				t.Fatal("fixture has no label")
			}
			res := ground.Check(ground.NewText(rep.GroundText), json.RawMessage(`{"label":"`+tt.phrase+`","ref":"QX7PLM"}`), nil, 0)
			got := []string{}
			for _, m := range res.Ungrounded {
				got = append(got, m.Pointer)
			}
			if want := []string{"/label"}; !reflect.DeepEqual(got, want) {
				t.Errorf("ungrounded = %v, want %v (a value of the document stays grounded)", got, want)
			}
		})
	}
}

func TestLabelPlain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"none", "Hello\nWorld\n", "Hello\nWorld\n"},
		{"quote without attribution", "Hi\n> a\n> b\nBye", "Hi\n[quoted earlier reply]\n> a\n> b\n[end of quoted earlier reply]\nBye"},
		{"quote with a blank line inside", "> a\n\n> b\nBye", "[quoted earlier reply]\n> a\n\n> b\n[end of quoted earlier reply]\nBye"},
		{"attribution wrapped on two lines", "On Fri, 9 Oct 2026 at 18:02, Ben\n<ben@example.com> wrote:\n> x\nBye",
			"[quoted earlier reply]\nOn Fri, 9 Oct 2026 at 18:02, Ben\n<ben@example.com> wrote:\n> x\n[end of quoted earlier reply]\nBye"},
		{"attribution without a quote", "On Fri Ben wrote:\nhello", "On Fri Ben wrote:\nhello"},
		{"CRLF lines", "Hi\r\n> a\r\nBye\r\n", "Hi\r\n[quoted earlier reply]\r\n> a\r\n[end of quoted earlier reply]\r\nBye\r\n"},
		{"forward marker, trailing newline", "Hi\n-----Original Message-----\nFrom: A\nSent: x\nTo: B\nSubject: s\n\ntext\n",
			"Hi\n[forwarded message]\n-----Original Message-----\nFrom: A\nSent: x\nTo: B\nSubject: s\n\ntext\n[end of forwarded message]\n"},
		{"forward marker, no trailing newline", "-----Original Message-----\ntext", "[forwarded message]\n-----Original Message-----\ntext\n[end of forwarded message]"},
		{"header block without marker", "Hi\n\nFrom: A\nDate: x\nSubject: s\n\ntext", "Hi\n\n[forwarded message]\nFrom: A\nDate: x\nSubject: s\n\ntext\n[end of forwarded message]"},
		{"a lone From line is not a header", "From: Berlin\nTo: Lisbon\n", "From: Berlin\nTo: Lisbon\n"},
		{"quote right after a marker belongs to it", "Begin forwarded message:\n\n> From: A\n> text\n",
			"[forwarded message]\nBegin forwarded message:\n\n> From: A\n> text\n[end of forwarded message]\n"},
		{"nested markers", "-----Original Message-----\na\n-----Original Message-----\nb",
			"[forwarded message]\n-----Original Message-----\na\n[forwarded message]\n-----Original Message-----\nb\n[end of forwarded message]\n[end of forwarded message]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := labelPlain(tt.in)
			if got != tt.want {
				t.Errorf("labelPlain(%q)\n = %q\nwant %q", tt.in, got, tt.want)
			}
			if stripLabels(got) != tt.in {
				t.Errorf("without labels: %q, want the input %q", stripLabels(got), tt.in)
			}
		})
	}
}

func TestLabelPlainDepthBound(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("-----Original Message-----\ntext\n", 10)
	got := labelPlain(in)
	if n := strings.Count(got, fwdStart); n != maxSectionDepth {
		t.Errorf("%d forwarded labels, want %d", n, maxSectionDepth)
	}
	if stripLabels(got) != in {
		t.Error("text was changed")
	}
}

func TestVisibleTextLabelled(t *testing.T) {
	t.Parallel()
	filler := strings.TrimSpace(strings.Repeat("word ", 100))
	tests := []struct {
		name, html, want string
	}{
		{"no section", `<p>a</p><p>b</p>`, "a\n\nb"},
		{"quoted blockquote", `<p>Thanks</p><blockquote type="cite"><p>old</p></blockquote><p>Ana</p>`,
			"Thanks\n\n[quoted earlier reply]\nold\n[end of quoted earlier reply]\n\nAna"},
		{"quote at the very start", `<blockquote type="cite">old</blockquote>`, "[quoted earlier reply]\nold\n[end of quoted earlier reply]"},
		{"empty quote is not labelled", `<p>a</p><blockquote type="cite"></blockquote><p>b</p>`, "a\n\nb"},
		{"nested quotes", `<blockquote type="cite">one<blockquote type="cite">two</blockquote></blockquote>`,
			"[quoted earlier reply]\none\n\n[quoted earlier reply]\ntwo\n[end of quoted earlier reply]\n[end of quoted earlier reply]"},
		{"to-end forward ends at the end of an enclosing quote",
			`<blockquote type="cite">` + filler + `<p>-----Original Message-----</p><p>y</p></blockquote><p>z</p>`,
			"[quoted earlier reply]\n" + filler + "\n\n[forwarded message]\n-----Original Message-----\n\ny\n[end of forwarded message]\n[end of quoted earlier reply]\n\nz"},
		{"a quote that begins with a marker is the forwarded message", `<blockquote type="cite"><p>-----Original Message-----</p><p>y</p></blockquote>`,
			"[forwarded message]\n-----Original Message-----\n\ny\n[end of forwarded message]"},
		{"hidden quote", `<p>a</p><blockquote type="cite" hidden>old</blockquote><p>b</p>`, "a\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := visibleTextLabelled([]byte(tt.html), maxVisibleText)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if plain := visibleText([]byte(tt.html), maxVisibleText); stripLabels(got) != plain {
				t.Errorf("without labels %q, want %q", stripLabels(got), plain)
			}
		})
	}
}

func TestVisibleTextLabelledDepthAndLimit(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat(`<blockquote type="cite">x`, 10)
	got := visibleTextLabelled([]byte(deep), maxVisibleText)
	if n := strings.Count(got, qStart); n != maxSectionDepth {
		t.Errorf("%d quoted labels, want %d", n, maxSectionDepth)
	}
	if plain := visibleText([]byte(deep), maxVisibleText); stripLabels(got) != plain {
		t.Error("text was changed")
	}
	// A body cut by the limit still ends with the truncation marker and never exceeds it.
	long := `<blockquote type="cite">` + strings.Repeat("word ", 100) + `</blockquote>`
	if got := visibleTextLabelled([]byte(long), 64); len(got) > 64+len(truncatedMarker) || !strings.HasSuffix(got, truncatedMarker) {
		t.Errorf("limit not respected: %d bytes", len(got))
	}
}

// TestEmailSectionLabelsCorpus: the corpus emails are single bookings, not forwards or
// replies, so none is labelled, and the text is today's output either way.
func TestEmailSectionLabelsCorpus(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*", "source.eml"))
	if err != nil || len(paths) < 40 {
		t.Fatalf("found %d corpus emails, %v", len(paths), err)
	}
	for _, path := range paths {
		doc := newEmail(t, path)
		for _, policy := range []HTMLPolicy{RawHTML, VisibleText} {
			plain := doc.email.text(policy)
			labelled := doc.email.labelledText(policy)
			if got := labelLines(labelled); len(got) != 0 {
				t.Errorf("%s (%s): labels %v on a single message", filepath.Base(filepath.Dir(path)), policy, got)
			}
			if stripLabels(labelled) != plain {
				t.Errorf("%s (%s): text without labels differs", filepath.Base(filepath.Dir(path)), policy)
			}
		}
	}
}

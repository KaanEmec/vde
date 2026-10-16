package document

import (
	"bytes"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxVisibleText caps visible_text output; longer output ends with truncatedMarker.
const (
	maxVisibleText  = 1 << 20
	truncatedMarker = "\n[truncated]"
)

func htmlText(src []byte, policy HTMLPolicy) string {
	if policy == VisibleText {
		return visibleText(src, maxVisibleText)
	}
	return rawHTML(src)
}

// rawHTML returns the HTML source with comments and the contents of script and style
// elements removed; every other byte passes through unchanged. The output is never
// longer than the input, so the document limit bounds it.
func rawHTML(src []byte) string {
	var b strings.Builder
	b.Grow(len(src))
	z := html.NewTokenizer(bytes.NewReader(src))
	inRaw := false // inside script or style
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken: // io.EOF: a bytes.Reader fails no other way
			return b.String()
		case html.CommentToken:
			continue
		case html.TextToken:
			if inRaw {
				continue
			}
		}
		b.Write(z.Raw()) // before TagName, which lowercases the buffer in place
		if tt == html.StartTagToken || tt == html.EndTagToken {
			name, _ := z.TagName()
			if a := atom.Lookup(name); a == atom.Script || a == atom.Style {
				inRaw = tt == html.StartTagToken
			}
		}
	}
}

// visibleText reduces HTML to the text a reader sees, in document order: blocks as
// paragraphs or lines, list items with a marker, table cells separated by tabs, links
// as "label (href)" for http, https and mailto targets, images as their alt text.
// Scripts, styles, the head (except the title), comments and hidden elements are
// dropped. Output beyond limit bytes is cut and ends with truncatedMarker.
func visibleText(src []byte, limit int) string {
	root, err := html.Parse(bytes.NewReader(src))
	if err != nil { // only reader errors, impossible with bytes.Reader
		return ""
	}
	w := &textWriter{max: limit}
	type frame struct {
		n     *html.Node
		exit  bool
		start int // output length when an <a> was entered
	}
	stack := []frame{{n: root}}
	pre := 0 // depth of <pre> elements
	for len(stack) > 0 && !w.full {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := f.n
		if f.exit {
			switch n.DataAtom {
			case atom.Pre:
				pre--
			case atom.A:
				w.link(attr(n, "href"), f.start)
			}
			w.block(blockBreak(n.DataAtom))
			continue
		}
		switch n.Type {
		case html.TextNode:
			if pre > 0 {
				w.verbatim(n.Data)
			} else {
				w.text(n.Data)
			}
			continue
		case html.ElementNode:
			if hidden(n) {
				continue
			}
			w.block(blockBreak(n.DataAtom))
			if !enterElement(w, n) {
				continue
			}
			if n.DataAtom == atom.Pre {
				pre++
			}
			stack = append(stack, frame{n: n, exit: true, start: w.b.Len()})
		case html.DocumentNode:
		default: // comments, doctype
			continue
		}
		var children []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			children = append(children, c)
		}
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, frame{n: children[i]})
		}
	}
	return w.b.String()
}

// enterElement writes what a visible element contributes before its children and
// reports whether to descend into it.
func enterElement(w *textWriter, n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Math,
		atom.Iframe, atom.Object, atom.Embed, atom.Select, atom.Input:
		return false
	case atom.Head:
		if t := findElement(n, atom.Title); t != nil {
			w.block(2)
			w.text(textContent(t))
			w.block(2)
		}
		return false
	case atom.Br:
		w.lineBreak()
		return false
	case atom.Img:
		w.text(" " + attr(n, "alt") + " ")
		return false
	case atom.Li:
		w.text(listMarker(n))
		w.space = true
	case atom.Td, atom.Th:
		for s := n.PrevSibling; s != nil; s = s.PrevSibling {
			if s.DataAtom == atom.Td || s.DataAtom == atom.Th {
				w.tab = true
				break
			}
		}
	}
	return true
}

// blockBreak is the number of newlines around a block element: 2 for paragraphs, 1 for
// lines, 0 for inline elements.
func blockBreak(a atom.Atom) int {
	switch a {
	case atom.P, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Blockquote,
		atom.Pre, atom.Ul, atom.Ol, atom.Dl, atom.Hr, atom.Figure:
		return 2
	case atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Nav,
		atom.Aside, atom.Main, atom.Li, atom.Table, atom.Tr, atom.Caption, atom.Dt, atom.Dd,
		atom.Address, atom.Center, atom.Form, atom.Fieldset, atom.Legend, atom.Figcaption,
		atom.Details, atom.Summary, atom.Body:
		return 1
	}
	return 0
}

// hidden reports the obvious ways an element is hidden from a reader.
func hidden(n *html.Node) bool {
	for _, a := range n.Attr {
		switch a.Key {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(a.Val), "true") {
				return true
			}
		case "style":
			s := strings.ToLower(strings.Join(strings.Fields(a.Val), ""))
			if strings.Contains(s, "display:none") || strings.Contains(s, "visibility:hidden") ||
				strings.Contains(s, "mso-hide:all") {
				return true
			}
		}
	}
	return false
}

func listMarker(li *html.Node) string {
	list := li.Parent
	for list != nil && list.DataAtom != atom.Ol && list.DataAtom != atom.Ul {
		list = list.Parent
	}
	if list == nil || list.DataAtom != atom.Ol {
		return "-"
	}
	n := 1
	if start, err := strconv.Atoi(strings.TrimSpace(attr(list, "start"))); err == nil {
		n = start
	}
	for s := li.PrevSibling; s != nil; s = s.PrevSibling {
		if s.DataAtom == atom.Li {
			n++
		}
	}
	return strconv.Itoa(n) + "."
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findElement(n *html.Node, a atom.Atom) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == a {
			return c
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

// linkTarget returns href when it is an absolute http, https or mailto URL.
func linkTarget(href string) string {
	href = strings.TrimSpace(href)
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return href
	}
	return ""
}

// textWriter accumulates visible text, collapsing whitespace and merging the separators
// between pieces: pending newlines win over a pending tab, which wins over a space.
type textWriter struct {
	b      strings.Builder
	max    int
	full   bool
	space  bool
	tab    bool
	breaks int // 0, 1 or 2 newlines
}

// invisible are zero-width and formatting characters email templates use as padding.
var invisible = strings.NewReplacer("\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "",
	"\u034f", "", "\u00ad", "", "\u2060", "")

func (w *textWriter) text(s string) {
	s = invisible.Replace(s)
	words := strings.Fields(s)
	if len(words) == 0 {
		if s != "" {
			w.space = true
		}
		return
	}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		w.space = true
	}
	w.write(strings.Join(words, " "))
	if r, _ := utf8.DecodeLastRuneInString(s); unicode.IsSpace(r) {
		w.space = true
	}
}

func (w *textWriter) verbatim(s string) {
	if s = invisible.Replace(s); s != "" {
		w.write(s)
	}
}

// link appends " (href)" to the label written since start, or the bare target when
// the link has no visible label. A label equal to its target is not repeated.
func (w *textWriter) link(href string, start int) {
	target := linkTarget(href)
	if target == "" || w.full {
		return
	}
	label := strings.TrimSpace(w.b.String()[start:])
	switch label {
	case target, strings.TrimPrefix(target, "mailto:"):
		return
	case "":
		w.text(target)
		return
	}
	w.space = true
	w.text("(" + target + ")")
}

// block requests n newlines before the next text, unless nothing was written yet.
func (w *textWriter) block(n int) {
	if w.b.Len() > 0 && n > w.breaks {
		w.breaks = n
	}
}

func (w *textWriter) lineBreak() {
	if w.b.Len() > 0 {
		w.breaks = min(w.breaks+1, 2)
	}
}

func (w *textWriter) write(s string) {
	if w.full {
		return
	}
	sep := ""
	if w.b.Len() > 0 {
		switch {
		case w.breaks > 0:
			sep = strings.Repeat("\n", w.breaks)
		case w.tab:
			sep = "\t"
		case w.space:
			sep = " "
		}
	}
	w.space, w.tab, w.breaks = false, false, 0
	s = sep + s
	if room := w.max - w.b.Len(); len(s) > room {
		for room > 0 && !utf8.RuneStart(s[room]) {
			room--
		}
		w.b.WriteString(s[:room])
		w.b.WriteString(truncatedMarker)
		w.full = true
		return
	}
	w.b.WriteString(s)
}

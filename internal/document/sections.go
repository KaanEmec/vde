package document

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Email section labels. A forwarded message or a quoted earlier reply inside an email
// body gets a label line before it and one after it, so the models know which part is
// the sender's own message. Labels only add lines: no text is removed, and removing the
// label lines gives exactly the text without labels. Detection is heuristic and fails
// safe: what is not recognised stays unlabelled, as before. A label carries no values and
// is not grounding text (Representation.GroundText is the text without labels).

// sectionKind is what a labelled section is.
type sectionKind uint8

const (
	sectionForwarded sectionKind = iota + 1
	sectionQuoted
)

func (k sectionKind) startLabel() string {
	if k == sectionForwarded {
		return "[forwarded message]"
	}
	return "[quoted earlier reply]"
}

func (k sectionKind) endLabel() string {
	if k == sectionForwarded {
		return "[end of forwarded message]"
	}
	return "[end of quoted earlier reply]"
}

// Bounds on the detection, so a hostile body cannot make it expensive.
const (
	maxSectionDepth   = 4       // nested labelled sections; deeper ones stay unlabelled
	sectionLookahead  = 400     // runes after a section opens in which its own header is not a new section
	prefixRunes       = 800     // text read from the start of an element to classify it
	prefixNodes       = 400     // nodes visited per element for that
	detectionBudget   = 2000000 // nodes visited by all classifications of one body
	markerPrefixRunes = 120
)

// sectionMark is an element that opens a labelled section. A bounded section ends where
// the element ends; a toEnd section (a "From: Sent:" block, "-----Original Message-----")
// runs to the end of the enclosing labelled section, else of the body.
type sectionMark struct {
	kind  sectionKind
	toEnd bool
}

// openSection and pendingLabel are the writer's state for labels not yet written.
type openSection struct {
	node *html.Node // the element of a bounded section, nil for a toEnd one
	kind sectionKind
}

type pendingLabel struct {
	text string
	sec  *openSection
	open bool // the label opens sec, rather than closing it
}

// openSection starts the section of n (a label before the next text). It returns false
// when the nesting limit is reached, and the element then stays unlabelled.
func (w *textWriter) openSection(n *html.Node, m sectionMark) bool {
	if len(w.sections) >= maxSectionDepth || w.full {
		return false
	}
	s := &openSection{kind: m.kind}
	if !m.toEnd {
		s.node = n
	}
	w.sections = append(w.sections, s)
	w.pending = append(w.pending, pendingLabel{text: m.kind.startLabel(), sec: s, open: true})
	return true
}

// closeSection ends the bounded section of n and any toEnd section opened inside it.
func (w *textWriter) closeSection(n *html.Node) {
	for len(w.sections) > 0 {
		top := w.sections[len(w.sections)-1]
		w.endSection()
		if top.node == n {
			return
		}
	}
}

// endSection pops the innermost section. A section that wrote no text between its two
// labels is dropped instead of labelled.
func (w *textWriter) endSection() {
	top := w.sections[len(w.sections)-1]
	w.sections = w.sections[:len(w.sections)-1]
	if last := len(w.pending) - 1; last >= 0 && w.pending[last].sec == top && w.pending[last].open {
		w.pending = w.pending[:last]
		return
	}
	w.pending = append(w.pending, pendingLabel{text: top.kind.endLabel(), sec: top})
}

// finish ends the sections still open at the end of the body and writes the labels that
// wait, each on its own line.
func (w *textWriter) finish() {
	for len(w.sections) > 0 {
		w.endSection()
	}
	if len(w.pending) == 0 || w.full {
		return
	}
	closing, opening := w.takeLabels()
	if w.b.Len() > 0 {
		w.b.WriteString("\n")
	}
	w.b.WriteString(strings.Trim(closing+"\n"+opening, "\n"))
}

// takeLabels returns the waiting labels and forgets them: closing is the labels that end
// sections, opening the ones that start sections, one per line each. They are written
// around the separator before the next text (closing hugging the text before it, opening
// the text after it); removing the label lines leaves the text and the separator as
// without labels.
func (w *textWriter) takeLabels() (closing, opening string) {
	var c, o []string
	for _, p := range w.pending {
		if p.open || len(o) > 0 {
			o = append(o, p.text)
		} else {
			c = append(c, p.text)
		}
	}
	w.pending = w.pending[:0]
	return strings.Join(c, "\n"), strings.Join(o, "\n")
}

// Patterns of the heuristics, matched against whitespace-normalised text.
var (
	// forwardMarker opens a forwarded or earlier message in the sender's mail client's own
	// words: Outlook, Gmail and Apple Mail.
	forwardMarker = regexp.MustCompile(`(?i)^(?:-{2,}\s*(?:Original Message|Forwarded message|Ursprüngliche Nachricht|Weitergeleitete Nachricht)\s*-{2,}|Begin forwarded message\s*:)`)
	// attribution is the line before a quoted reply.
	attribution = regexp.MustCompile(`(?i)^(?:On|Am|Le)\s.{3,300}?\s(?:wrote|schrieb|a écrit)\s*:\s*$`)
	attrTail    = regexp.MustCompile(`(?i)\s(?:wrote|schrieb|a écrit)\s*:\s*$`)
	attrHead    = regexp.MustCompile(`(?i)^(?:On|Am|Le)\s`)
	headerFrom  = regexp.MustCompile(`(?i)^(?:from|von|de)\s*:`)
	headerSent  = regexp.MustCompile(`(?i)\b(?:sent|date|gesendet|datum)\s*:`)
	headerTo    = regexp.MustCompile(`(?i)\b(?:to|an)\s*:`)
	headerSubj  = regexp.MustCompile(`(?i)\b(?:subject|betreff)\s*:`)
)

// headerBlockAt reports whether text starts a forwarded message's header: a From line,
// then at least two of the sent or date, to and subject lines.
func headerBlockAt(text string) bool {
	if !headerFrom.MatchString(text) {
		return false
	}
	n := 0
	for _, re := range []*regexp.Regexp{headerSent, headerTo, headerSubj} {
		if re.MatchString(text) {
			n++
		}
	}
	return n >= 2
}

// htmlSections finds the sections of an HTML email body: on the parsed tree, in document
// order, by the markers mail clients leave (classes and attributes first, then their
// own phrases). Everything is bounded; an unrecognised structure is left alone.
//
//   - blockquote type="cite" (Apple Mail) and the gmail_quote class: a quoted earlier
//     reply, or a forwarded message when it begins with "Forwarded message" or follows
//     "Begin forwarded message:";
//   - a block that begins with "-----Original Message-----", "Begin forwarded message:"
//     or a "From: Sent: To: Subject:" header (Outlook): a forwarded message to the end.
func htmlSections(root *html.Node) map[*html.Node]sectionMark {
	type frame struct {
		n    *html.Node
		exit bool
		kind byte // 'b' bounded section, 'e' toEnd section (suppresses nested toEnd detection)
	}
	var (
		marks     = map[*html.Node]sectionMark{}
		stack     = []frame{{n: root}}
		budget    = detectionBudget
		pos       int // runes of visible text read so far
		prev      string
		starts    []int // pos at which each open bounded section began
		endSuppr  int   // open toEnd elements
		lastToEnd = -1 << 30
	)
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := f.n
		if f.exit {
			switch f.kind {
			case 'b':
				starts = starts[:len(starts)-1]
			case 'e':
				endSuppr--
			}
			continue
		}
		switch n.Type {
		case html.TextNode:
			t := normalized(n.Data)
			if t != "" {
				pos += utf8.RuneCountInString(t)
				prev = tail(prev+" "+t, 120)
			}
			continue
		case html.ElementNode:
			if hidden(n) || invisibleElement(n.DataAtom) {
				continue
			}
			kind := byte(0)
			if budget > 0 {
				mark, ok, used := classify(n, prev, &marksState{
					nearStart:     len(starts) > 0 && pos-starts[len(starts)-1] < sectionLookahead,
					endSuppressed: endSuppr > 0,
					coveredByEnd:  pos-lastToEnd < sectionLookahead/2,
				})
				budget -= used
				if ok {
					marks[n] = mark
					if mark.toEnd {
						kind, lastToEnd = 'e', pos
						endSuppr++
					} else {
						kind = 'b'
						starts = append(starts, pos)
					}
					stack = append(stack, frame{n: n, exit: true, kind: kind})
				}
			}
		case html.DocumentNode:
		default:
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
	if len(marks) == 0 {
		return nil
	}
	return marks
}

// marksState is what classify needs to know about the walk so far.
type marksState struct {
	nearStart     bool // inside the first lines of an open labelled section
	endSuppressed bool // inside a toEnd section's own marker or header
	coveredByEnd  bool // a toEnd section began just before
}

// classify decides whether n opens a labelled section; used is the work it did.
func classify(n *html.Node, prev string, st *marksState) (m sectionMark, ok bool, used int) {
	forwarded := sectionMark{kind: sectionForwarded}
	switch n.DataAtom {
	case atom.Blockquote:
		if strings.EqualFold(strings.TrimSpace(attr(n, "type")), "cite") || hasClass(n, "gmail_quote") {
			text, _, used := prefixText(n, markerPrefixRunes)
			switch {
			case forwardMarker.MatchString(text):
				return forwarded, true, used
			case endsWithBeginForwarded(prev) && st.coveredByEnd:
				return sectionMark{}, false, used // the marker block before it already opened the section
			case endsWithBeginForwarded(prev):
				return forwarded, true, used
			}
			return sectionMark{kind: sectionQuoted}, true, used
		}
	case atom.Div:
		if hasClass(n, "gmail_quote") {
			text, _, used := prefixText(n, prefixRunes)
			switch {
			case forwardMarker.MatchString(text):
				return forwarded, true, used
			case hasChildBlockquote(n):
				return sectionMark{}, false, used // the blockquote inside is the quoted reply
			case attribution.MatchString(firstSentence(text)):
				return sectionMark{kind: sectionQuoted}, true, used
			}
			return sectionMark{}, false, used
		}
	}
	// Blocks that open a forwarded message running to the end.
	switch n.DataAtom {
	case atom.Div, atom.P, atom.Blockquote, atom.Pre, atom.Center, atom.Section, atom.Article, atom.Address:
	default:
		return sectionMark{}, false, 0
	}
	if st.endSuppressed || st.nearStart {
		return sectionMark{}, false, 0
	}
	text, complete, used := prefixText(n, prefixRunes)
	switch {
	case complete && isMarkerLine(text):
		// The marker block itself, not a wrapper around more: the label stands before it.
		return sectionMark{kind: sectionForwarded, toEnd: true}, true, used
	case st.coveredByEnd, endsWithMarker(prev):
		return sectionMark{}, false, used
	case headerBlockAt(text):
		return sectionMark{kind: sectionForwarded, toEnd: true}, true, used
	}
	return sectionMark{}, false, used
}

// isMarkerLine reports whether text is, whole, a forwarded or original message marker.
func isMarkerLine(text string) bool {
	return text != "" && forwardMarker.FindString(text) == text
}

func endsWithBeginForwarded(prev string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(prev)), "begin forwarded message:")
}

func endsWithMarker(prev string) bool {
	p := strings.TrimSpace(prev)
	for _, tail := range []string{"begin forwarded message:", "original message-----", "forwarded message ---------", "forwarded message---------"} {
		if strings.HasSuffix(strings.ToLower(p), tail) {
			return true
		}
	}
	return false
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if strings.EqualFold(c, class) {
			return true
		}
	}
	return false
}

func hasChildBlockquote(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Blockquote {
			return true
		}
	}
	return false
}

// firstSentence is text up to its first line of an attribution's length: the part a
// "On ... wrote:" line fits in.
func firstSentence(text string) string {
	if i := strings.Index(text, "wrote:"); i >= 0 {
		return text[:i+len("wrote:")]
	}
	if i := strings.Index(text, "schrieb:"); i >= 0 {
		return text[:i+len("schrieb:")]
	}
	return text
}

// invisibleElement reports elements the text writer does not descend into.
func invisibleElement(a atom.Atom) bool {
	switch a {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Math,
		atom.Iframe, atom.Object, atom.Embed, atom.Select, atom.Input, atom.Head:
		return true
	}
	return false
}

// normalized collapses whitespace and drops the zero-width padding the writer drops.
func normalized(s string) string {
	return strings.Join(strings.Fields(invisible.Replace(s)), " ")
}

// tail returns the last n bytes of s, cut at a rune start.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// prefixText returns the first maxRunes runes of the visible text under n, whitespace
// collapsed and pieces joined by a space, whether n's text is complete, and the nodes
// visited (at most prefixNodes).
func prefixText(n *html.Node, maxRunes int) (text string, complete bool, used int) {
	var (
		b     strings.Builder
		runes int
		stack = []*html.Node{}
	)
	for c := n.LastChild; c != nil; c = c.PrevSibling {
		stack = append(stack, c)
	}
	for len(stack) > 0 {
		if used++; used > prefixNodes {
			return b.String(), false, used
		}
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch c.Type {
		case html.TextNode:
			t := normalized(c.Data)
			if t == "" {
				continue
			}
			if runes > 0 {
				b.WriteByte(' ')
				runes++
			}
			if room := maxRunes - runes; utf8.RuneCountInString(t) > room {
				for i := range t {
					if room == 0 {
						t = t[:i]
						break
					}
					room--
				}
				b.WriteString(t)
				return b.String(), false, used
			}
			runes += utf8.RuneCountInString(t)
			b.WriteString(t)
		case html.ElementNode:
			if hidden(c) || invisibleElement(c.DataAtom) {
				continue
			}
			for k := c.LastChild; k != nil; k = k.PrevSibling {
				stack = append(stack, k)
			}
		}
	}
	return b.String(), true, used
}

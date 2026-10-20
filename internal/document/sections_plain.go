package document

import (
	"regexp"
	"strings"
)

// Plain-text bodies: the same sections as sections.go, found line by line.

var (
	ruleLine   = regexp.MustCompile(`^[_=*-]{5,}$`)
	keySent    = regexp.MustCompile(`(?i)^(?:sent|date|gesendet|datum)\s*:`)
	keyTo      = regexp.MustCompile(`(?i)^(?:to|an)\s*:`)
	keySubject = regexp.MustCompile(`(?i)^(?:subject|betreff)\s*:`)
)

// maxHeaderLines is how far a From line's header block reaches.
const maxHeaderLines = 10

// labelPlain returns body with a label line before and after each forwarded message and
// quoted earlier reply it recognises: a marker line ("-----Original Message-----",
// "Begin forwarded message:") or a "From: Sent: To: Subject:" block opens a forwarded
// message that runs to the end; a run of ">" lines, with its "On ... wrote:" line, is a
// quoted earlier reply. Removing the label lines gives body.
func labelPlain(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines)+4)
	var (
		open       int // forwarded sections running to the end
		lastMarker = -1
		changed    bool
	)
	// covered reports whether only blank and rule lines lie between the last marker line
	// and line i, so a header or quote there belongs to the marker's section.
	covered := func(i int) bool {
		if lastMarker < 0 {
			return false
		}
		for _, l := range lines[lastMarker+1 : i] {
			if t := strings.TrimSpace(l); t != "" && !ruleLine.MatchString(t) {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		switch {
		case isMarkerLine(t):
			if open < maxSectionDepth {
				out = append(out, sectionForwarded.startLabel()+lineEnd(line))
				open++
				changed = true
			}
			lastMarker = i
			out = append(out, line)
		case headerFrom.MatchString(t) && open < maxSectionDepth && !covered(i) && plainHeaderBlock(lines[i:]):
			label := sectionForwarded.startLabel() + lineEnd(line)
			if n := len(out); n > 0 && ruleLine.MatchString(strings.TrimSpace(out[n-1])) {
				out = append(out[:n-1], label, out[n-1]) // before the rule line above the header
			} else {
				out = append(out, label)
			}
			open++
			changed = true
			out = append(out, line)
		default:
			last, ok := quotedRun(lines, i)
			if !ok {
				out = append(out, line)
				continue
			}
			if covered(i) { // the forwarded content, quoted: no section of its own
				out = append(out, lines[i:last+1]...)
				i = last
				continue
			}
			out = append(out, sectionQuoted.startLabel()+lineEnd(line))
			out = append(out, lines[i:last+1]...)
			out = append(out, sectionQuoted.endLabel()+lineEnd(lines[last]))
			changed = true
			i = last
		}
	}
	if !changed {
		return body
	}
	ref := lines[len(lines)-1]
	if ref == "" && len(lines) > 1 {
		ref = lines[len(lines)-2]
	}
	for range open {
		end := sectionForwarded.endLabel() + lineEnd(ref)
		if n := len(out); n > 0 && out[n-1] == "" {
			out = append(out[:n-1], end, "") // before the body's final newline
		} else {
			out = append(out, end)
		}
	}
	return strings.Join(out, "\n")
}

// lineEnd is "\r" when ref ends with one, so a label line in a CRLF body ends like the
// lines around it.
func lineEnd(ref string) string {
	if strings.HasSuffix(ref, "\r") {
		return "\r"
	}
	return ""
}

// plainHeaderBlock reports whether lines open with a forwarded message's header: a From
// line, then within maxHeaderLines non-blank lines at least two of Sent or Date, To and
// Subject.
func plainHeaderBlock(lines []string) bool {
	var sent, to, subject bool
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if i >= maxHeaderLines || t == "" {
			break
		}
		sent = sent || keySent.MatchString(t)
		to = to || keyTo.MatchString(t)
		subject = subject || keySubject.MatchString(t)
	}
	n := 0
	for _, found := range []bool{sent, to, subject} {
		if found {
			n++
		}
	}
	return n >= 2
}

// quotedRun reports whether a quoted reply starts at lines[i] and returns its last line:
// an attribution line ("On ... wrote:", possibly wrapped onto two lines) followed by ">"
// lines, or ">" lines alone.
func quotedRun(lines []string, i int) (last int, ok bool) {
	start := i
	t := strings.TrimSpace(lines[i])
	switch {
	case attribution.MatchString(t):
		start = i + 1
	case attrHead.MatchString(t) && !attrTail.MatchString(t) && i+1 < len(lines) &&
		attrTail.MatchString(strings.TrimSpace(lines[i+1])) && len(t)+len(lines[i+1]) < 400:
		start = i + 2
	case !isQuoteLine(lines[i]):
		return 0, false
	}
	if start != i { // one blank line may separate the attribution from the quote
		if start < len(lines) && strings.TrimSpace(lines[start]) == "" {
			start++
		}
		if start >= len(lines) || !isQuoteLine(lines[start]) {
			return 0, false
		}
	}
	last = start
	for k := start + 1; k < len(lines); k++ {
		switch {
		case isQuoteLine(lines[k]):
			last = k
		case strings.TrimSpace(lines[k]) == "" && k+1 < len(lines) && isQuoteLine(lines[k+1]):
			// a blank line inside the quote
		default:
			return last, true
		}
	}
	return last, true
}

func isQuoteLine(l string) bool { return strings.HasPrefix(strings.TrimLeft(l, " \t"), ">") }

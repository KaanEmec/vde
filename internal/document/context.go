package document

import (
	"fmt"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/provider"
)

// PreparationVersion names how the service lays a document out before a model reads it:
// the context line and page markers, and later the email section labels and the HTML
// table output. It is hashed into service_version and bumped with every such change.
const PreparationVersion = "prep/2"

// contextLine is the one line that opens what the models read, built only from what the
// service knows: "type: email · sent: 2026-10-10T09:14:00+02:00 · pages: 3". sent is an
// email's Date header as RFC 3339 with its own offset, left out when absent or not
// parseable; pages is left out when zero (unknown or not paged). The job's creation time
// is never used. The line is not grounding text (Representation.GroundText).
func (d *Document) contextLine(pages int) string {
	var b strings.Builder
	b.WriteString("type: " + d.typeName())
	if d.email != nil && d.email.sent != "" {
		b.WriteString(" · sent: " + d.email.sent)
	}
	if pages > 0 {
		fmt.Fprintf(&b, " · pages: %d", pages)
	}
	return b.String()
}

func (d *Document) typeName() string {
	switch d.BaseType() {
	case "text/html":
		return "html"
	case "text/markdown":
		return "markdown"
	case "text/plain":
		return "text"
	}
	return d.Category.String()
}

// sentTime renders a message's Date header as RFC 3339 keeping the original offset, or
// "" when it is missing or unparseable.
func sentTime(t time.Time, err error) string {
	if err != nil || t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// withContext puts the context line and a blank line before text.
func withContext(line, text string) string { return line + "\n\n" + text }

// pageMarker is the line that opens page n (1-based): in a text layer and before each
// page image.
func pageMarker(n int) string { return fmt.Sprintf("--- page %d ---", n) }

// pageParts is the context line as a text part, then each page image behind its marker.
func pageParts(line string, images []Page) []provider.Part {
	parts := make([]provider.Part, 0, 1+2*len(images))
	parts = append(parts, provider.Part{Text: line})
	for i, p := range images {
		parts = append(parts, provider.Part{Text: pageMarker(i + 1)},
			provider.Part{Image: &provider.Blob{MediaType: p.MediaType, Data: p.Data}})
	}
	return parts
}

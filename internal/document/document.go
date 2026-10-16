package document

import (
	"fmt"
	"mime"
	"net/http"

	"github.com/kaanemec/vde/internal/config"
)

// Contract §4 limits, used when config.Limits leaves a value at zero.
const (
	DefaultDocumentBytes = 25 << 20
	DefaultPageBytes     = 10 << 20
	DefaultPages         = 20
)

// Document is one validated document of an accepted media type.
type Document struct {
	// MediaType is normalized: lowercase, no parameters, except "; charset=utf-8" on
	// text types, whose Data has been decoded to UTF-8.
	MediaType string
	Category  Category
	Data      []byte // the document; UTF-8 text for CategoryText, the raw message for email
	Filename  string // as the caller gave it, informational only
	Pages     []Page // page images in request order, possibly none

	// Attachments lists an email's attachments (metadata only, never read). Nil for
	// other documents.
	Attachments []Attachment

	email *email // parsed message, for CategoryEmail
}

// Page is one page image supplied by the caller.
type Page struct {
	MediaType string // image/png or image/jpeg, from sniffing
	Data      []byte
}

// Input is a document as submitted. MediaType is the document part's Content-Type with
// its parameters. A page's MediaType is its declared Content-Type and may be empty.
type Input struct {
	MediaType string
	Data      []byte
	Filename  string
	Pages     []Page
}

// New validates in against the accepted media types and limits and returns the
// document. Binary types must match their sniffed type; text types must decode to valid
// UTF-8; email is parsed. Errors are *Error with the code the API maps to a status.
func New(in Input, limits config.Limits) (*Document, error) {
	docLimit := orDefault(limits.DocumentBytes, DefaultDocumentBytes)
	if int64(len(in.Data)) > docLimit {
		return nil, refuse(CodePayloadTooLarge, "document exceeds %d bytes", docLimit)
	}
	if len(in.Data) == 0 {
		return nil, refuse(CodeBadRequest, "document is empty")
	}
	mt, params, err := LookupMediaType(in.MediaType)
	if err != nil {
		return nil, err
	}
	pages, err := validatePages(in.Pages, limits)
	if err != nil {
		return nil, err
	}
	doc := &Document{
		MediaType: mt.Name,
		Category:  mt.Category,
		Data:      in.Data,
		Filename:  in.Filename,
		Pages:     pages,
	}
	switch mt.Category {
	case CategoryText:
		text, err := toUTF8(in.Data, params["charset"], mt.Name == "text/html", true)
		if err != nil {
			return nil, err
		}
		doc.Data = text
		doc.MediaType = mime.FormatMediaType(mt.Name, map[string]string{"charset": "utf-8"})
	case CategoryEmail:
		if doc.email, err = parseEmail(in.Data); err != nil {
			return nil, err
		}
		doc.Attachments = doc.email.attachments
	default:
		if !sniff(mt.Name, in.Data) {
			return nil, refuse(CodeUnsupportedMediaType, "content does not match media type %q", mt.Name)
		}
	}
	return doc, nil
}

// BaseType is MediaType without parameters.
func (d *Document) BaseType() string {
	name, _, err := mime.ParseMediaType(d.MediaType)
	if err != nil {
		return d.MediaType
	}
	return name
}

// Text returns what the models read for a text or email document under the HTML
// policy: plain text and markdown as they are, HTML per policy, email as its header
// block and best body. It fails for PDF and image documents.
func (d *Document) Text(policy HTMLPolicy) (string, error) {
	policy, err := policy.resolve()
	if err != nil {
		return "", err
	}
	switch d.Category {
	case CategoryText:
		if d.BaseType() == "text/html" {
			return htmlText(d.Data, policy), nil
		}
		return string(d.Data), nil
	case CategoryEmail:
		if d.email == nil {
			return "", fmt.Errorf("document: email was not parsed by New")
		}
		return d.email.text(policy), nil
	}
	return "", fmt.Errorf("document: %s has no text representation", d.Category)
}

func validatePages(pages []Page, limits config.Limits) ([]Page, error) {
	maxPages := orDefault(limits.Pages, DefaultPages)
	if len(pages) > maxPages {
		return nil, refuse(CodePayloadTooLarge, "more than %d pages", maxPages)
	}
	pageLimit := orDefault(limits.PageBytes, DefaultPageBytes)
	out := make([]Page, 0, len(pages))
	for i, p := range pages {
		n := i + 1
		if int64(len(p.Data)) > pageLimit {
			return nil, refuse(CodePayloadTooLarge, "page %d exceeds %d bytes", n, pageLimit)
		}
		if len(p.Data) == 0 {
			return nil, refuse(CodeBadRequest, "page %d is empty", n)
		}
		got := http.DetectContentType(p.Data)
		if got != "image/png" && got != "image/jpeg" {
			return nil, refuse(CodeUnsupportedMediaType, "page %d is not PNG or JPEG", n)
		}
		if p.MediaType != "" {
			declared, _, err := mime.ParseMediaType(p.MediaType)
			if a, ok := aliases[declared]; ok {
				declared = a
			}
			if err != nil || declared != got {
				return nil, refuse(CodeUnsupportedMediaType, "page %d content does not match its media type", n)
			}
		}
		out = append(out, Page{MediaType: got, Data: p.Data})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func orDefault[T int | int64](v T, def T) T {
	if v <= 0 {
		return def
	}
	return v
}

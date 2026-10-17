package document

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/provider"
)

// PDFEngine opens PDFs for their text layer and page images. PDFEngineOf adapts a
// *pdf.Engine; tests use a stub.
type PDFEngine interface {
	Open(ctx context.Context, data []byte) (PDFDoc, error)
}

// PDFDoc is an open PDF (*pdf.Doc). Pages are 0-based. Errors wrap the pdf package's
// sentinels (pdf.ErrTooManyPages, pdf.ErrEncrypted, pdf.ErrInvalid, ...).
type PDFDoc interface {
	PageCount() int
	Text(ctx context.Context, page int) (string, error)
	Render(ctx context.Context, page, dpi int) ([]byte, error)
	Close()
}

// PDFEngineOf returns e as a PDFEngine, or nil for a nil engine.
func PDFEngineOf(e *pdf.Engine) PDFEngine {
	if e == nil {
		return nil
	}
	return pdfEngine{e}
}

type pdfEngine struct{ e *pdf.Engine }

func (p pdfEngine) Open(ctx context.Context, data []byte) (PDFDoc, error) {
	d, err := p.e.Open(ctx, data)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// PDFPolicy is how a PDF reaches groups that cannot read application/pdf natively
// (documents.pdf, ADR 0010). Without an Engine, or under the native mode, PDFs are
// treated as in ADR 0006: native readers or caller pages only.
type PDFPolicy struct {
	Mode         string // config.PDFModeAuto (also for ""), PDFModeNative, PDFModeText or PDFModePages
	DPI          int    // rendering resolution
	MaxPages     int    // more pages are refused; zero means no limit here (the engine has its own)
	MinTextChars int    // auto: text when every page has this many characters after trimming
	Engine       PDFEngine
}

// NewPDFPolicy returns the policy of a configuration for one application: its own
// applications[].documents.pdf.mode when set, else documents.pdf.mode.
func NewPDFPolicy(c config.PDF, appMode string, engine PDFEngine) PDFPolicy {
	mode := c.Mode
	if appMode != "" {
		mode = appMode
	}
	return PDFPolicy{Mode: mode, DPI: c.DPI, MaxPages: c.MaxPages, MinTextChars: c.MinTextCharsPerPage, Engine: engine}
}

// String names the parts of the policy that change what a model reads (mode, dpi and
// minimum characters), for service_version. The engine and limits are not part of it.
func (p PDFPolicy) String() string {
	return fmt.Sprintf("%s/dpi=%d/min_chars=%d", p.mode(), p.DPI, p.MinTextChars)
}

func (p PDFPolicy) mode() string {
	if p.Mode == "" {
		return config.PDFModeAuto
	}
	return p.Mode
}

// active reports whether the service reads or renders PDFs itself under p.
func (p PDFPolicy) active() bool { return p.Engine != nil && p.mode() != config.PDFModeNative }

// textLayer is a PDF's text, one entry per page.
type textLayer []string

// readTextLayer opens data and reads the text of every page. The caller closes the
// returned document.
func readTextLayer(ctx context.Context, e PDFEngine, data []byte, maxPages int) (PDFDoc, textLayer, error) {
	d, err := e.Open(ctx, data)
	if err != nil {
		return nil, nil, pdfError(err, maxPages)
	}
	if maxPages > 0 && d.PageCount() > maxPages {
		d.Close()
		return nil, nil, pdfError(pdf.ErrTooManyPages, maxPages)
	}
	layer := make(textLayer, d.PageCount())
	for i := range layer {
		if layer[i], err = d.Text(ctx, i); err != nil {
			d.Close()
			return nil, nil, pdfError(err, maxPages)
		}
	}
	return d, layer, nil
}

// pdfError turns the pdf package's refusals into *Error (415 unsupported_document) and
// wraps anything else (a timeout, a closed engine) as an internal error.
func pdfError(err error, maxPages int) error {
	switch {
	case errors.Is(err, pdf.ErrTooManyPages): // still matches pdf.ErrTooManyPages, so the API can name the limit
		if maxPages > 0 {
			return fmt.Errorf("%w: %w", refuse(CodeUnsupportedDocument, "the PDF has more than %d pages (documents.pdf.max_pages) "+
				"and no configured model reads PDFs natively", maxPages), pdf.ErrTooManyPages)
		}
		return fmt.Errorf("%w: %w", refuse(CodeUnsupportedDocument, "the PDF has too many pages to prepare"), pdf.ErrTooManyPages)
	case errors.Is(err, pdf.ErrPageTooLarge):
		return refuse(CodeUnsupportedDocument, "a PDF page is too large to render")
	case errors.Is(err, pdf.ErrEncrypted), errors.Is(err, pdf.ErrInvalid), errors.Is(err, pdf.ErrTooLarge):
		return refuse(CodeUnsupportedDocument, "the PDF cannot be read (encrypted or invalid)")
	}
	return fmt.Errorf("document: pdf: %w", err)
}

// rich reports whether every page has at least min characters after trimming.
func (l textLayer) rich(minChars int) bool {
	for _, t := range l {
		if utf8.RuneCountInString(strings.TrimSpace(t)) < minChars {
			return false
		}
	}
	return true
}

// modelText is what a model reads for a PDF sent as text: every page under a
// "--- page N ---" marker, pages separated by a form feed.
func (l textLayer) modelText() string {
	var b strings.Builder
	for i, t := range l {
		if i > 0 {
			b.WriteString("\n\f\n")
		}
		fmt.Fprintf(&b, "--- page %d ---\n", i+1)
		b.WriteString(strings.TrimSpace(t))
	}
	return b.String()
}

// groundText is the text grounding reads: the pages without markers (a marker's page
// number would ground any small number). It is groundable when the pages average at
// least min characters, and at least one.
func (l textLayer) groundText(minChars int) (string, bool) {
	pages := make([]string, len(l))
	total := 0
	for i, t := range l {
		pages[i] = strings.TrimSpace(t)
		total += utf8.RuneCountInString(pages[i])
	}
	if total == 0 || len(l) == 0 || total/len(l) < minChars {
		return "", false
	}
	return strings.Join(pages, "\n\n"), true
}

// selectPDF chooses a text-layer or rendered-pages representation under p for a PDF that
// neither a native reader nor the caller's pages could serve. The PDF is opened once,
// and the same bytes and policy always give the same representation. The engine
// instance is held only while reading: from Open through the text layer and, when the
// pages are tried, their rendering; the image conversion runs after it is released.
func selectPDF(ctx context.Context, doc *Document, extractor, judge []config.Capability, images ImagePolicy, p PDFPolicy) (Representation, error) {
	d, layer, err := readTextLayer(ctx, p.Engine, doc.Data, p.MaxPages)
	if err != nil {
		return Representation{}, err
	}
	defer d.Close() // Close is idempotent; the paths below close it as soon as they can
	ground, groundable := layer.groundText(p.MinTextChars)

	var order []Kind
	switch p.mode() {
	case config.PDFModeText:
		order = []Kind{Text}
	case config.PDFModePages:
		order = []Kind{Pages}
	default: // auto
		if layer.rich(p.MinTextChars) {
			order = []Kind{Text, Pages}
		} else {
			order = []Kind{Pages}
		}
	}
	for _, kind := range order {
		if kind == Text {
			c := candidate{kind: Text, mediaTypes: []string{"text/plain"}}
			if reads(extractor, c) && reads(judge, c) {
				return Representation{
					Kind: Text, Source: SourceTextLayer, MediaTypes: c.mediaTypes,
					Parts:      []provider.Part{{Text: layer.modelText()}},
					GroundText: ground, Groundable: groundable,
				}, nil
			}
			continue
		}
		if !imagesReadable(extractor, judge, images) {
			continue
		}
		rendered := make([]Page, d.PageCount())
		for i := range rendered {
			png, err := d.Render(ctx, i, p.DPI)
			if err != nil {
				return Representation{}, pdfError(err, p.MaxPages)
			}
			rendered[i] = Page{MediaType: "image/png", Data: png}
		}
		d.Close() // every page is rendered: release the instance before converting
		cands, err := pageCandidates(rendered, images)
		if err != nil {
			return Representation{}, err
		}
		for _, c := range cands {
			if reads(extractor, c) && reads(judge, c) {
				return Representation{
					Kind: Pages, Source: SourceRendered, MediaTypes: c.mediaTypes,
					Parts:      imageParts(c.images),
					GroundText: ground, Groundable: groundable,
				}, nil
			}
		}
	}
	reason := "a role's group reads neither its text nor rendered page images"
	switch {
	case p.mode() == config.PDFModeText:
		reason = "a role's group does not read text"
	case p.mode() == config.PDFModePages:
		reason = "a role's group does not read rendered page images"
	case !layer.rich(p.MinTextChars):
		reason = fmt.Sprintf("a page has fewer than %d characters of text (documents.pdf.min_text_chars_per_page), "+
			"and a role's group does not read rendered page images", p.MinTextChars)
	}
	return Representation{}, refuse(CodeUnsupportedDocument,
		"no representation of application/pdf is readable by both the extractor and the judge group "+
			"(documents.pdf.mode: %s): %s", p.mode(), reason)
}

// imagesReadable reports whether both roles read a rendered page in one of the types it
// can take: PNG as rendered, or the image policy's conversion target.
func imagesReadable(extractor, judge []config.Capability, images ImagePolicy) bool {
	types := []string{"image/png"}
	if images.Enabled {
		types = append(types, images.resolve().ConvertTo)
	}
	for _, mt := range types {
		c := candidate{kind: Pages, mediaTypes: []string{mt}}
		if reads(extractor, c) && reads(judge, c) {
			return true
		}
	}
	return false
}

// GroundingText returns the document text that grounding checks a candidate against,
// and whether there is any (ADR 0009). It is rep's GroundText when Select computed it
// (text, HTML as visible text, email, and PDFs it read as text or rendered). For a PDF
// sent natively or as caller pages it opens the PDF with p's engine and reads the text
// layer, which is groundable when its pages average at least p.MinTextChars characters;
// call it only when the judge policy needs grounding, so a deployment that never does
// never starts pdfium. Images and PDFs without an engine are not groundable. The text is
// never logged.
//
// A PDF that cannot be read is not groundable (err nil). err is set only when the read
// could not happen now: the engine had no instance free (pdf.ErrBusy) or ctx ended; the
// caller retries later rather than run the job without its grounding text.
func GroundingText(ctx context.Context, doc *Document, rep Representation, p PDFPolicy) (string, bool, error) {
	if rep.Groundable {
		return rep.GroundText, true, nil
	}
	if doc.Category != CategoryPDF || p.Engine == nil || rep.Source == SourceTextLayer || rep.Source == SourceRendered {
		return "", false, nil // already measured, or nothing to read
	}
	d, layer, err := readTextLayer(ctx, p.Engine, doc.Data, 0)
	if err != nil {
		if errors.Is(err, pdf.ErrBusy) || ctx.Err() != nil {
			return "", false, err
		}
		return "", false, nil
	}
	d.Close()
	text, ok := layer.groundText(p.MinTextChars)
	return text, ok, nil
}

package document

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// HTMLPolicy is how HTML (a text/html document or an email's HTML body) is given to the
// models (ADR 0006). It is part of service_version.
type HTMLPolicy string

// HTML policies. The empty policy means RawHTML.
const (
	RawHTML     HTMLPolicy = "raw_html"     // the source, minus comments, scripts and styles
	VisibleText HTMLPolicy = "visible_text" // the text a reader sees, links kept
)

// String returns the policy name; the empty policy is "raw_html".
func (p HTMLPolicy) String() string {
	if p == "" {
		return string(RawHTML)
	}
	return string(p)
}

func (p HTMLPolicy) resolve() (HTMLPolicy, error) {
	switch p {
	case "", RawHTML:
		return RawHTML, nil
	case VisibleText:
		return VisibleText, nil
	}
	return "", fmt.Errorf("document: unknown HTML policy %q", string(p))
}

// ResolveHTMLPolicy returns the policy in effect for an application: its own
// applications[].documents.html when set, else documents.html, else raw_html.
func ResolveHTMLPolicy(global, app string) (HTMLPolicy, error) {
	p := HTMLPolicy(global)
	if app != "" {
		p = HTMLPolicy(app)
	}
	return p.resolve()
}

// Kind is what the models read.
type Kind int

// Representation kinds. The zero value is not a kind.
const (
	Native Kind = iota + 1 // the document itself, as a document or image part
	Pages                  // the caller's page images, as image parts in order
	Text                   // one text part
)

// String is the representation name hashed into service_version.
func (k Kind) String() string {
	switch k {
	case Native:
		return "native"
	case Pages:
		return "pages"
	case Text:
		return "text"
	}
	return "unknown"
}

// Source is where a representation's content comes from.
type Source string

// Sources. Kind says what the models read; Source says how the service got it.
const (
	SourceGiven     Source = "given"      // the document or the caller's pages as submitted
	SourceTextLayer Source = "text_layer" // a PDF's text layer, read by the service (ADR 0010)
	SourceRendered  Source = "rendered"   // a PDF's pages, rendered by the service (ADR 0010)
)

// Representation is what both roles read, decided once at submit. Parts is built once:
// the extractor and the judge are given this same slice.
type Representation struct {
	Kind  Kind
	Parts []provider.Part
	// MediaTypes are the types of Parts; the router uses an endpoint-model pair only
	// when it accepts all of them. Text is always text/plain.
	MediaTypes []string
	// HTMLPolicy is the policy applied to HTML content, "" when the document has none.
	HTMLPolicy HTMLPolicy
	// Source is SourceGiven, or for a PDF the service prepared, SourceTextLayer (Kind
	// Text) or SourceRendered (Kind Pages).
	Source Source

	// GroundText is the document text the grounding check reads (ADR 0009), which can
	// differ from what the models read: visible text for HTML under either policy, the
	// header block and visible body for email, the text layer for a PDF. It is never
	// sent to a model unless Kind is Text, and never logged. Groundable is false when
	// there is no such text: images, caller pages, and PDFs whose text layer is too
	// thin or was not read (see GroundingText for PDFs sent natively or as pages).
	GroundText string
	Groundable bool
}

// Label is the representation as stored in job metadata: the kind, plus the source when
// the service prepared it ("text+text_layer", "pages+rendered").
func (r Representation) Label() string {
	if r.Source == "" || r.Source == SourceGiven {
		return r.Kind.String()
	}
	return r.Kind.String() + "+" + string(r.Source)
}

// Select chooses the representation from the document and the capabilities of the
// extractor and judge groups (config.Capabilities), in this order:
//
//  1. text, markdown, HTML (under policy) and email → Text;
//  2. PDF or image → Native when a pair accepts the type, else Pages when pages were
//     supplied and a pair accepts their types;
//  3. the judge group must read the same choice, else the next option is tried.
//
// When nothing fits both roles it returns ErrUnsupportedDocument.
//
// Images are converted here under images, once, and the converted bytes are the Parts
// both roles read:
//
//   - an image that needs downscaling or EXIF rotation is offered converted, then as
//     given, so a group that only reads the original type still works;
//   - a WebP is offered as given, then converted, so a group whose pairs lack
//     image/webp (or whose roles disagree on it) reads the JPEG or PNG conversion;
//   - page images follow the same rules, converted or as given as a whole.
//
// Representation.MediaTypes reports the types after conversion. A disabled image policy
// leaves every image as given.
//
// A PDF policy with an engine (ADR 0010) lets the service prepare a PDF that neither a
// native reader nor the caller's pages serve: under auto, its text layer when every page
// has at least MinTextChars characters, else its pages rendered at DPI (then through the
// image policy); text and pages force one; native keeps ADR 0006's refusal. A PDF with
// more than MaxPages pages is refused then. With a zero PDFPolicy (no engine) such a PDF
// needs caller pages.
//
// A PDF the service prepares is opened, read and rendered under ctx, so cancellation
// (shutdown, a gone client) and a job's deadline stop the engine. An engine with no
// instance free reports pdf.ErrBusy (wrapped, not a refusal): try again later.
func Select(ctx context.Context, doc *Document, extractor, judge []config.Capability, policy HTMLPolicy, images ImagePolicy, pdf PDFPolicy) (Representation, error) {
	policy, err := policy.resolve()
	if err != nil {
		return Representation{}, err
	}
	cands, err := candidates(ctx, doc, images)
	if err != nil {
		return Representation{}, err
	}
	for _, c := range cands {
		if !reads(extractor, c) || !reads(judge, c) {
			continue
		}
		return build(doc, c, policy)
	}
	if doc.Category == CategoryPDF && pdf.active() {
		return selectPDF(ctx, doc, extractor, judge, images, pdf)
	}
	return Representation{}, refuse(CodeUnsupportedDocument,
		"no representation of %s is readable by both the extractor and the judge group", doc.BaseType())
}

// candidate is a representation option before its parts are built.
type candidate struct {
	kind       Kind
	mediaTypes []string
	images     []Page // the image parts of a Native image or Pages candidate
}

func candidates(ctx context.Context, doc *Document, images ImagePolicy) ([]candidate, error) {
	switch doc.Category {
	case CategoryText, CategoryEmail:
		return []candidate{{kind: Text, mediaTypes: []string{"text/plain"}}}, nil
	}
	var out []candidate
	if doc.Category == CategoryImage {
		forms, err := images.variants(ctx, doc.Data, doc.BaseType())
		if err != nil {
			return nil, err
		}
		for _, f := range forms {
			out = append(out, candidate{kind: Native, mediaTypes: []string{f.MediaType}, images: []Page{f}})
		}
	} else {
		out = append(out, candidate{kind: Native, mediaTypes: []string{doc.BaseType()}})
	}
	if len(doc.Pages) == 0 {
		return out, nil
	}
	pages, err := pageCandidates(ctx, doc.Pages, images)
	if err != nil {
		return nil, err
	}
	return append(out, pages...), nil
}

// pageCandidates offers page images converted by the image policy, then as given when
// the conversion changed any page.
func pageCandidates(ctx context.Context, pages []Page, images ImagePolicy) ([]candidate, error) {
	converted := make([]Page, len(pages))
	changed := false
	for i, p := range pages {
		forms, err := images.variants(ctx, p.Data, p.MediaType)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i+1, err)
		}
		converted[i] = forms[0]
		changed = changed || forms[0].MediaType != p.MediaType || !bytes.Equal(forms[0].Data, p.Data)
	}
	out := []candidate{pagesCandidate(converted)}
	if changed {
		out = append(out, pagesCandidate(pages))
	}
	return out, nil
}

func pagesCandidate(pages []Page) candidate {
	var types []string
	for _, p := range pages {
		if !slices.Contains(types, p.MediaType) {
			types = append(types, p.MediaType)
		}
	}
	return candidate{kind: Pages, mediaTypes: types, images: pages}
}

// reads reports whether one endpoint-model pair of the group reads every media type of
// c: natively for Native and Pages, as text (text/plain) for Text.
func reads(caps []config.Capability, c candidate) bool {
	via := config.ViaNative
	if c.kind == Text {
		via = config.ViaText
	}
	var common []string
	for i, mt := range c.mediaTypes {
		var pairs []string
		for _, capability := range caps {
			if capability.MediaType == mt && capability.Via == via {
				pairs = capability.Pairs
				break
			}
		}
		if i == 0 {
			common = slices.Clone(pairs)
		} else {
			common = slices.DeleteFunc(common, func(p string) bool { return !slices.Contains(pairs, p) })
		}
	}
	return len(common) > 0
}

func build(doc *Document, c candidate, policy HTMLPolicy) (Representation, error) {
	r := Representation{Kind: c.kind, MediaTypes: c.mediaTypes, Source: SourceGiven}
	switch c.kind {
	case Native:
		// No page count: a native document is not opened here.
		r.Parts = []provider.Part{{Text: doc.contextLine(0)}}
		if doc.Category == CategoryPDF {
			r.Parts = append(r.Parts, provider.Part{Document: &provider.Blob{MediaType: doc.BaseType(), Data: doc.Data}})
		} else {
			r.Parts = append(r.Parts, imageParts(c.images)...)
		}
	case Pages:
		r.Parts = pageParts(doc.contextLine(len(c.images)), c.images)
	case Text:
		text, err := doc.Text(policy)
		if err != nil {
			return Representation{}, err
		}
		model := text
		if doc.Category == CategoryEmail {
			model = doc.email.labelledText(policy) // section labels; GroundText stays without them
		}
		r.Parts = []provider.Part{{Text: withContext(doc.contextLine(0), model)}}
		if doc.hasHTML() {
			r.HTMLPolicy = policy
		}
		r.GroundText = text
		if policy != VisibleText && doc.hasHTML() {
			if r.GroundText, err = doc.Text(VisibleText); err != nil {
				return Representation{}, err
			}
		}
		r.Groundable = strings.TrimSpace(r.GroundText) != ""
	}
	return r, nil
}

func imageParts(images []Page) []provider.Part {
	parts := make([]provider.Part, len(images))
	for i, p := range images {
		parts[i] = provider.Part{Image: &provider.Blob{MediaType: p.MediaType, Data: p.Data}}
	}
	return parts
}

func (d *Document) hasHTML() bool {
	if d.Category == CategoryEmail {
		return d.email != nil && d.email.html
	}
	return d.BaseType() == "text/html"
}

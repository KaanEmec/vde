package document

import (
	"fmt"
	"slices"

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
}

// Select chooses the representation from the document and the capabilities of the
// extractor and judge groups (config.Capabilities), in this order:
//
//  1. text, markdown, HTML (under policy) and email → Text;
//  2. PDF or image → Native when a pair accepts the type, else Pages when pages were
//     supplied and a pair accepts their types;
//  3. the judge group must read the same choice, else the next option is tried.
//
// When nothing fits both roles it returns ErrUnsupportedDocument. The service never
// renders pages (ADR 0006).
func Select(doc *Document, extractor, judge []config.Capability, policy HTMLPolicy) (Representation, error) {
	policy, err := policy.resolve()
	if err != nil {
		return Representation{}, err
	}
	for _, c := range candidates(doc) {
		if !reads(extractor, c) || !reads(judge, c) {
			continue
		}
		return build(doc, c, policy)
	}
	return Representation{}, refuse(CodeUnsupportedDocument,
		"no representation of %s is readable by both the extractor and the judge group", doc.BaseType())
}

// candidate is a representation option before its parts are built.
type candidate struct {
	kind       Kind
	mediaTypes []string
}

func candidates(doc *Document) []candidate {
	switch doc.Category {
	case CategoryText, CategoryEmail:
		return []candidate{{Text, []string{"text/plain"}}}
	}
	out := []candidate{{Native, []string{doc.BaseType()}}}
	if len(doc.Pages) > 0 {
		var types []string
		for _, p := range doc.Pages {
			if !slices.Contains(types, p.MediaType) {
				types = append(types, p.MediaType)
			}
		}
		out = append(out, candidate{Pages, types})
	}
	return out
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
	r := Representation{Kind: c.kind, MediaTypes: c.mediaTypes}
	switch c.kind {
	case Native:
		blob := &provider.Blob{MediaType: doc.BaseType(), Data: doc.Data}
		if doc.Category == CategoryPDF {
			r.Parts = []provider.Part{{Document: blob}}
		} else {
			r.Parts = []provider.Part{{Image: blob}}
		}
	case Pages:
		r.Parts = make([]provider.Part, len(doc.Pages))
		for i, p := range doc.Pages {
			r.Parts[i] = provider.Part{Image: &provider.Blob{MediaType: p.MediaType, Data: p.Data}}
		}
	case Text:
		text, err := doc.Text(policy)
		if err != nil {
			return Representation{}, err
		}
		r.Parts = []provider.Part{{Text: text}}
		if doc.hasHTML() {
			r.HTMLPolicy = policy
		}
	}
	return r, nil
}

func (d *Document) hasHTML() bool {
	if d.Category == CategoryEmail {
		return d.email != nil && d.email.html
	}
	return d.BaseType() == "text/html"
}

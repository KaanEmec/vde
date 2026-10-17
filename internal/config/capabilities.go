package config

import (
	"slices"
	"strings"
)

// How a group reads a media type, from the accepts lists of its endpoint-model pairs.
const (
	ViaNative = "native" // a pair accepts the type: the model gets the document itself
	ViaPages  = "pages"  // PDF only: a pair accepts image/png or image/jpeg, so the caller's page images work
	ViaText   = "text"   // text types are given as text: a pair accepts text/plain or the type itself
)

// requiredMediaTypes are the document types contract §4 says the service MUST accept.
var requiredMediaTypes = []string{"application/pdf", "image/jpeg", "image/png", "text/html", "text/plain"}

// Capability says how a group reads one document media type.
type Capability struct {
	MediaType string
	Required  bool     // a MUST type of contract §4
	Via       string   // ViaNative, ViaPages, ViaText, or "" when the group cannot read it
	Pairs     []string // "endpoint/model" pairs providing Via, in group order
}

// Capabilities reports for every known document media type how group reads it. It
// returns nil for an undefined group. A PDF without a native reader reports ViaPages when
// a pair reads PNG or JPEG: caller pages, or pages the service renders under
// documents.pdf.mode (ADR 0010); a text-only group may still read its text layer.
func (c *Config) Capabilities(group string) []Capability {
	g, ok := c.Groups[group]
	if !ok || g == nil {
		return nil
	}
	accepting := func(types ...string) []string {
		var pairs []string
		for _, entry := range g.Endpoints {
			e, ok := c.Endpoints[entry.Endpoint]
			if !ok || e == nil {
				continue
			}
			m, ok := e.Models[entry.Model]
			if !ok {
				continue
			}
			for _, t := range types {
				if slices.Contains(m.Accepts, t) {
					pairs = append(pairs, entry.Endpoint+"/"+entry.Model)
					break
				}
			}
		}
		return pairs
	}

	out := make([]Capability, 0, len(knownMediaTypes))
	for _, mt := range knownMediaTypes {
		capability := Capability{MediaType: mt, Required: slices.Contains(requiredMediaTypes, mt)}
		switch {
		case strings.HasPrefix(mt, "text/"):
			capability.Via, capability.Pairs = ViaText, accepting("text/plain", mt)
		default:
			capability.Via, capability.Pairs = ViaNative, accepting(mt)
			if len(capability.Pairs) == 0 && mt == "application/pdf" {
				capability.Via, capability.Pairs = ViaPages, accepting("image/png", "image/jpeg")
			}
		}
		if len(capability.Pairs) == 0 {
			capability.Via = ""
		}
		out = append(out, capability)
	}
	return out
}

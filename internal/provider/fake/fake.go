package fake

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/provider"
)

// Token counts reported on every answer. The fake does not simulate token accounting.
const (
	InputTokens  = 100
	OutputTokens = 20
)

// ErrExhausted is the cause of the permanent failure returned when no step is left that
// matches a request.
var ErrExhausted = errors.New("fake: script exhausted")

// Step is one scripted reply.
type Step struct {
	Role   string          // match requests whose System contains Role; "" matches all
	Model  string          // match requests for this model; "" matches all
	Delay  time.Duration   // wait before replying; the wait ends early when ctx is done
	Answer string          // the answer text, used when Err is nil
	Err    *provider.Error // the failure to return instead of an answer
	Repeat bool            // the step is never consumed
	// Truncated marks the answer as cut off at the output-token limit.
	Truncated bool
}

// Script is an ordered list of steps.
type Script []Step

// Answer returns a step answering text.
func Answer(text string) Step { return Step{Answer: text} }

// Fail returns a step failing with kind and HTTP status (0 for none).
func Fail(kind provider.Kind, status int) Step {
	return Step{Err: &provider.Error{Kind: kind, Status: status, Message: "fake: scripted failure"}}
}

// Call is one recorded request.
type Call struct {
	Request provider.Request // a deep copy of the request as received
	Parts   []PartInfo       // one per Request.Parts entry
}

// PartInfo summarizes one part without its content.
type PartInfo struct {
	Kind      string // text, image or document
	MediaType string // text/plain for text parts
	Size      int    // bytes
}

// Provider is a scripted provider.Provider. It is safe for concurrent use.
type Provider struct {
	mu       sync.Mutex
	steps    Script
	consumed []bool
	calls    []Call
}

// New returns a provider replaying script. The script is copied.
func New(script Script) *Provider {
	return &Provider{steps: slices.Clone(script), consumed: make([]bool, len(script))}
}

// Complete records req, takes the first matching step, waits its delay and replies.
func (p *Provider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	p.mu.Lock()
	p.calls = append(p.calls, record(req))
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return provider.Response{}, err
	}
	step, ok := p.take(req)
	p.mu.Unlock()
	if !ok {
		return provider.Response{}, &provider.Error{Kind: provider.Permanent, Err: ErrExhausted}
	}

	if step.Delay > 0 {
		timer := time.NewTimer(step.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return provider.Response{}, ctx.Err()
		case <-timer.C:
		}
	}
	if step.Err != nil {
		e := *step.Err // a copy per call: callers may hold errors across goroutines
		return provider.Response{}, &e
	}
	return provider.Response{
		Text:         step.Answer,
		InputTokens:  InputTokens,
		OutputTokens: OutputTokens,
		Truncated:    step.Truncated,
	}, nil
}

// take returns the first unconsumed step matching req. p.mu must be held.
func (p *Provider) take(req provider.Request) (Step, bool) {
	for i, s := range p.steps {
		if p.consumed[i] || (s.Model != "" && s.Model != req.Model) || !strings.Contains(req.System, s.Role) {
			continue
		}
		if !s.Repeat {
			p.consumed[i] = true
		}
		return s, true
	}
	return Step{}, false
}

// Calls returns the requests received so far, in arrival order.
func (p *Provider) Calls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// Remaining returns the number of steps not consumed yet, not counting Repeat steps.
func (p *Provider) Remaining() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for i, s := range p.steps {
		if !s.Repeat && !p.consumed[i] {
			n++
		}
	}
	return n
}

func record(req provider.Request) Call {
	c := Call{Request: req, Parts: make([]PartInfo, len(req.Parts))}
	c.Request.Parts = make([]provider.Part, len(req.Parts))
	for i, part := range req.Parts {
		switch {
		case part.Image != nil:
			c.Parts[i] = PartInfo{Kind: "image", MediaType: part.Image.MediaType, Size: len(part.Image.Data)}
			part.Image = cloneBlob(part.Image)
		case part.Document != nil:
			c.Parts[i] = PartInfo{Kind: "document", MediaType: part.Document.MediaType, Size: len(part.Document.Data)}
			part.Document = cloneBlob(part.Document)
		default:
			c.Parts[i] = PartInfo{Kind: "text", MediaType: "text/plain", Size: len(part.Text)}
		}
		c.Request.Parts[i] = part
	}
	return c
}

func cloneBlob(b *provider.Blob) *provider.Blob {
	return &provider.Blob{MediaType: b.MediaType, Data: bytes.Clone(b.Data)}
}

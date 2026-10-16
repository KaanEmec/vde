package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/anthropic"
	"github.com/kaanemec/vde/internal/provider/bedrock"
	"github.com/kaanemec/vde/internal/provider/classifier"
	"github.com/kaanemec/vde/internal/provider/gemini"
	"github.com/kaanemec/vde/internal/provider/openai"
	"github.com/kaanemec/vde/internal/provider/vertex"
)

// newProvider returns the adapter for an endpoint's provider type. Fake endpoints have
// no adapter of their own: they need --provider-script.
func newProvider(e *config.Endpoint) (provider.Provider, error) {
	switch e.Provider {
	case "anthropic":
		return anthropic.New(e), nil
	case "openai_compatible":
		return openai.New(e), nil
	case "gemini":
		return gemini.New(e), nil
	case "bedrock":
		c, err := bedrock.New(e)
		if err != nil {
			return nil, err
		}
		return c, nil
	case "vertex":
		c, err := vertex.New(e)
		if err != nil {
			return nil, err
		}
		return c, nil
	case config.ProviderClassifier:
		return classifier.New(e), nil
	case "fake":
		return nil, errors.New("provider fake needs --provider-script")
	}
	return nil, fmt.Errorf("unknown provider %q", e.Provider)
}

// Roles as observed on a call. The framings (internal/extract/framings) open with
// "You are the extractor" and "You are the judge".
const (
	roleExtractor = "extractor"
	roleJudge     = "judge"
)

func roleOf(system string) string {
	if strings.HasPrefix(system, "You are the judge") {
		return roleJudge
	}
	return roleExtractor
}

// observer counts the calls and tokens of one run and, when verbose, writes one line per
// call to w. Nothing it writes carries content: role, endpoint, model, outcome, tokens.
type observer struct {
	w io.Writer // nil when not verbose

	mu     sync.Mutex
	calls  int
	input  int
	output int
}

func (o *observer) logf(format string, args ...any) {
	if o.w == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.w, "vde: "+format+"\n", args...)
}

func (o *observer) record(role, endpoint, model string, resp provider.Response, err error) {
	o.mu.Lock()
	o.calls++
	o.input += resp.InputTokens
	o.output += resp.OutputTokens
	o.mu.Unlock()
	if err == nil {
		o.logf("%s call %s/%s: ok, %d input and %d output tokens", role, endpoint, model, resp.InputTokens, resp.OutputTokens)
		return
	}
	status := ""
	var pe *provider.Error
	if errors.As(err, &pe) && pe.Status != 0 {
		status = fmt.Sprintf(" (status %d)", pe.Status)
	}
	o.logf("%s call %s/%s: %s failure%s", role, endpoint, model, provider.Classify(err), status)
}

func (o *observer) usage() (calls, input, output int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls, o.input, o.output
}

// observed wraps an endpoint's provider to report each call to an observer.
type observed struct {
	endpoint string
	p        provider.Provider
	o        *observer
}

func (p observed) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	resp, err := p.p.Complete(ctx, req)
	p.o.record(roleOf(req.System), p.endpoint, req.Model, resp, err)
	return resp, err
}

// providerFactory returns the build function for extract.NewRoles: every endpoint gets
// its adapter, or script when one is given, wrapped to report to o.
func providerFactory(cfg *config.Config, script provider.Provider, o *observer) func(*config.Endpoint) (provider.Provider, error) {
	names := make(map[*config.Endpoint]string, len(cfg.Endpoints))
	for name, e := range cfg.Endpoints {
		names[e] = name
	}
	return func(e *config.Endpoint) (provider.Provider, error) {
		p := script
		if p == nil {
			var err error
			if p, err = newProvider(e); err != nil {
				return nil, err
			}
		}
		return observed{endpoint: names[e], p: p, o: o}, nil
	}
}

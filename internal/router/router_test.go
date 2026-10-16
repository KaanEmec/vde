package router

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// spec is one endpoint with one model, served by a fake.
type spec struct {
	name    string
	accepts []string
	timeout time.Duration
	script  fake.Script
}

// newGroup builds a group named "g" over specs, in order, with no backoff.
func newGroup(t *testing.T, failover config.Failover, specs ...spec) (*Group, []*fake.Provider) {
	t.Helper()
	cfg := &config.Config{Endpoints: map[string]*config.Endpoint{}}
	g := &config.Group{Failover: failover}
	fakes := map[*config.Endpoint]*fake.Provider{}
	var ordered []*fake.Provider
	for _, s := range specs {
		accepts := s.accepts
		if accepts == nil {
			accepts = []string{"text/plain", "image/png", "application/pdf"}
		}
		e := &config.Endpoint{
			Provider: "fake",
			Timeout:  s.timeout,
			Models:   map[string]config.Model{s.name + "-model": {Accepts: accepts}},
		}
		cfg.Endpoints[s.name] = e
		g.Endpoints = append(g.Endpoints, config.GroupEntry{Endpoint: s.name, Model: s.name + "-model"})
		f := fake.New(s.script)
		fakes[e] = f
		ordered = append(ordered, f)
	}
	group, err := NewGroup("g", g, cfg, func(e *config.Endpoint) (provider.Provider, error) { return fakes[e], nil })
	if err != nil {
		t.Fatalf("NewGroup() error = %v", err)
	}
	group.backoff = 0
	return group, ordered
}

func callCounts(fakes []*fake.Provider) []int {
	n := make([]int, len(fakes))
	for i, f := range fakes {
		n[i] = len(f.Calls())
	}
	return n
}

func retryAfterStep(d time.Duration) fake.Step {
	s := fake.Fail(provider.Retryable, 429)
	s.Err.RetryAfter = d
	return s
}

var (
	textReq = provider.Request{Parts: []provider.Part{{Text: "instructions"}}}
	pdfReq  = provider.Request{Parts: []provider.Part{
		{Text: "instructions"},
		{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte("%PDF")}},
	}}
)

func TestGroup_Complete(t *testing.T) {
	t.Parallel()
	ok := fake.Script{fake.Answer("ok")}
	down := fake.AlwaysRetryable(503)
	tests := []struct {
		name       string
		failover   config.Failover
		specs      []spec
		req        provider.Request
		wantServed string // "" when an error is expected
		wantErr    error
		wantStatus int // status of the *provider.Error wrapped in the error, 0 to skip
		wantCalls  []int
	}{
		{
			name:       "first succeeds",
			specs:      []spec{{name: "a", script: ok}, {name: "b", script: ok}},
			req:        textReq,
			wantServed: "a/a-model",
			wantCalls:  []int{1, 0},
		},
		{
			name:       "first fails retryable then second succeeds",
			specs:      []spec{{name: "a", script: down}, {name: "b", script: ok}},
			req:        textReq,
			wantServed: "b/b-model",
			wantCalls:  []int{1, 1},
		},
		{
			name:       "all fail retryable",
			specs:      []spec{{name: "a", script: down}, {name: "b", script: fake.AlwaysRetryable(502)}},
			req:        textReq,
			wantErr:    ErrGroupUnavailable,
			wantStatus: 502,
			wantCalls:  []int{1, 1},
		},
		{
			name:       "permanent stops",
			specs:      []spec{{name: "a", script: fake.AlwaysPermanent(400)}, {name: "b", script: ok}},
			req:        textReq,
			wantErr:    ErrPermanent,
			wantStatus: 400,
			wantCalls:  []int{1, 0},
		},
		{
			name:       "permanent fails over when the group allows it",
			failover:   config.Failover{FailoverOnPermanent: true},
			specs:      []spec{{name: "a", script: fake.AlwaysPermanent(400)}, {name: "b", script: ok}},
			req:        textReq,
			wantServed: "b/b-model",
			wantCalls:  []int{1, 1},
		},
		{
			name:       "all permanent with failover is unavailable",
			failover:   config.Failover{FailoverOnPermanent: true},
			specs:      []spec{{name: "a", script: fake.AlwaysPermanent(400)}, {name: "b", script: fake.AlwaysPermanent(422)}},
			req:        textReq,
			wantErr:    ErrGroupUnavailable,
			wantStatus: 422,
			wantCalls:  []int{1, 1},
		},
		{
			name:       "pair skipped for media type",
			specs:      []spec{{name: "a", accepts: []string{"image/png"}, script: ok}, {name: "b", script: ok}},
			req:        pdfReq,
			wantServed: "b/b-model",
			wantCalls:  []int{0, 1},
		},
		{
			name:       "text parts need no capability",
			specs:      []spec{{name: "a", accepts: []string{"image/png"}, script: ok}},
			req:        textReq,
			wantServed: "a/a-model",
			wantCalls:  []int{1},
		},
		{
			name:      "no pair accepts the media type",
			specs:     []spec{{name: "a", accepts: []string{"image/png"}, script: ok}},
			req:       pdfReq,
			wantErr:   ErrPermanent,
			wantCalls: []int{0},
		},
		{
			name:       "max tries bounds the call",
			failover:   config.Failover{MaxTries: 2},
			specs:      []spec{{name: "a", script: down}, {name: "b", script: down}, {name: "c", script: ok}},
			req:        textReq,
			wantErr:    ErrGroupUnavailable,
			wantStatus: 503,
			wantCalls:  []int{1, 1, 0},
		},
		{
			name: "endpoint timeout moves on",
			specs: []spec{
				{name: "a", timeout: 20 * time.Millisecond, script: fake.Script{{Delay: time.Hour, Answer: "late"}}},
				{name: "b", script: ok},
			},
			req:        textReq,
			wantServed: "b/b-model",
			wantCalls:  []int{1, 1},
		},
		{
			name: "short retry-after retries the same pair once",
			specs: []spec{
				{name: "a", script: fake.Script{retryAfterStep(20 * time.Millisecond), fake.Answer("ok")}},
				{name: "b", script: ok},
			},
			req:        textReq,
			wantServed: "a/a-model",
			wantCalls:  []int{2, 0},
		},
		{
			name:     "same pair retried at most once",
			failover: config.Failover{MaxTries: 3},
			specs: []spec{
				{name: "a", script: fake.Script{retryAfterStep(time.Millisecond), retryAfterStep(time.Millisecond), fake.Answer("ok")}},
				{name: "b", script: ok},
			},
			req:        textReq,
			wantServed: "b/b-model",
			wantCalls:  []int{2, 1},
		},
		{
			name: "retry-after retry counts as a try",
			specs: []spec{
				{name: "a", script: fake.Script{retryAfterStep(time.Millisecond), retryAfterStep(time.Millisecond)}},
				{name: "b", script: ok},
			},
			req:        textReq,
			wantErr:    ErrGroupUnavailable,
			wantStatus: 429,
			wantCalls:  []int{2, 0},
		},
		{
			name:     "retry-after beyond the time budget skips to the next pair",
			failover: config.Failover{MaxTotalTime: time.Second},
			specs: []spec{
				{name: "a", script: fake.Script{retryAfterStep(5 * time.Second), fake.Answer("ok")}},
				{name: "b", script: ok},
			},
			req:        textReq,
			wantServed: "b/b-model",
			wantCalls:  []int{1, 1},
		},
		{
			name:     "retry-after over 10s skips to the next pair",
			failover: config.Failover{MaxTotalTime: time.Minute},
			specs: []spec{
				{name: "a", script: fake.Script{retryAfterStep(11 * time.Second), fake.Answer("ok")}},
				{name: "b", script: ok},
			},
			req:        textReq,
			wantServed: "b/b-model",
			wantCalls:  []int{1, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, fakes := newGroup(t, tt.failover, tt.specs...)
			resp, served, err := g.Complete(context.Background(), tt.req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Complete() error = %v, want %v", err, tt.wantErr)
				}
				var pe *provider.Error
				if tt.wantStatus != 0 && (!errors.As(err, &pe) || pe.Status != tt.wantStatus) {
					t.Errorf("Complete() error = %v, want it to wrap status %d", err, tt.wantStatus)
				}
			} else if err != nil || served.String() != tt.wantServed || resp.Text != "ok" {
				t.Fatalf("Complete() = %q, %s, %v, want ok from %s", resp.Text, served, err, tt.wantServed)
			}
			if got := callCounts(fakes); !slices.Equal(got, tt.wantCalls) {
				t.Errorf("calls per pair = %v, want %v", got, tt.wantCalls)
			}
		})
	}
}

func TestGroup_Complete_setsModelPerPair(t *testing.T) {
	t.Parallel()
	g, fakes := newGroup(t, config.Failover{},
		spec{name: "a", script: fake.AlwaysRetryable(503)}, spec{name: "b", script: fake.Script{fake.Answer("ok")}})
	if _, _, err := g.Complete(context.Background(), provider.Request{Model: "ignored"}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"a-model", "b-model"} {
		if got := fakes[i].Calls()[0].Request.Model; got != want {
			t.Errorf("pair %d got model %q, want %q", i, got, want)
		}
	}
}

func TestGroup_Complete_setsMaxTokens(t *testing.T) {
	t.Parallel()
	g, fakes := newGroup(t, config.Failover{},
		spec{name: "a", script: fake.AlwaysRetryable(503)}, spec{name: "b", script: fake.Script{fake.Answer("ok"), fake.Answer("ok")}})
	g.pairs[1].maxOut = 99
	if _, _, err := g.Complete(context.Background(), provider.Request{}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{config.DefaultMaxOutputTokens, 99} {
		if got := fakes[i].Calls()[0].Request.MaxTokens; got != want {
			t.Errorf("pair %d got MaxTokens %d, want %d", i, got, want)
		}
	}
	if _, _, err := g.Complete(context.Background(), provider.Request{MaxTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if got := fakes[1].Calls()[1].Request.MaxTokens; got != 5 {
		t.Errorf("a request's own MaxTokens became %d, want 5", got)
	}
}

func TestGroup_Complete_maxTotalTime(t *testing.T) {
	t.Parallel()
	slow := fake.Script{{Delay: time.Hour, Answer: "late", Repeat: true}}
	g, fakes := newGroup(t, config.Failover{MaxTotalTime: 50 * time.Millisecond},
		spec{name: "a", timeout: time.Hour, script: slow}, spec{name: "b", timeout: time.Hour, script: slow})
	start := time.Now()
	_, _, err := g.Complete(context.Background(), textReq)
	if !errors.Is(err, ErrGroupUnavailable) {
		t.Fatalf("Complete() error = %v, want ErrGroupUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Complete() took %s, want about 50ms", elapsed)
	}
	if got := callCounts(fakes); !slices.Equal(got, []int{1, 0}) {
		t.Errorf("calls per pair = %v, want [1 0]: no call after the budget is spent", got)
	}
	if !g.Healthy() {
		t.Error("Healthy() = false: a call cut short by the budget must not mark the pair down")
	}
}

func TestGroup_Complete_callerContext(t *testing.T) {
	t.Parallel()
	slow := fake.Script{{Delay: time.Hour, Answer: "late", Repeat: true}}
	tests := []struct {
		name      string
		ctx       func() (context.Context, context.CancelFunc)
		specs     []spec
		backoff   time.Duration
		wantErr   error
		wantCalls []int
	}{
		{
			name: "job deadline cuts a call short",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 30*time.Millisecond)
			},
			specs:     []spec{{name: "a", script: slow}, {name: "b", script: slow}},
			wantErr:   context.DeadlineExceeded,
			wantCalls: []int{1, 0},
		},
		{
			name: "done context calls nothing",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			specs:     []spec{{name: "a", script: fake.Script{fake.Answer("ok")}}},
			wantErr:   context.Canceled,
			wantCalls: []int{0},
		},
		{
			name: "cancel during backoff calls nothing more",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(30*time.Millisecond, cancel)
				return ctx, cancel
			},
			specs:     []spec{{name: "a", script: fake.AlwaysRetryable(503)}, {name: "b", script: fake.Script{fake.Answer("ok")}}},
			backoff:   time.Hour,
			wantErr:   context.Canceled,
			wantCalls: []int{1, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, fakes := newGroup(t, config.Failover{MaxTotalTime: time.Minute}, tt.specs...)
			g.backoff = tt.backoff
			ctx, cancel := tt.ctx()
			defer cancel()
			_, _, err := g.Complete(ctx, textReq)
			if err != tt.wantErr { //nolint:errorlint // the caller's context error must come back unchanged
				t.Errorf("Complete() error = %v, want exactly %v", err, tt.wantErr)
			}
			if got := callCounts(fakes); !slices.Equal(got, tt.wantCalls) {
				t.Errorf("calls per pair = %v, want %v", got, tt.wantCalls)
			}
		})
	}
}

func TestGroup_Complete_concurrent(t *testing.T) {
	t.Parallel()
	g, _ := newGroup(t, config.Failover{},
		spec{name: "a", script: fake.AlwaysRetryable(503)},
		spec{name: "b", script: fake.Script{{Answer: "ok", Repeat: true}}})
	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Go(func() { _, _, errs[i] = g.Complete(context.Background(), textReq) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Errorf("Complete() errors = %v", err)
	}
	if h := g.Health(); h[0].Failures != 1 {
		t.Errorf("pair a failures = %d, want 1: concurrent failures in one window count once", h[0].Failures)
	}
}

func TestNewGroup_errors(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Endpoints: map[string]*config.Endpoint{
		"e": {Models: map[string]config.Model{"m": {Accepts: []string{"text/plain"}}}},
	}}
	okBuild := func(*config.Endpoint) (provider.Provider, error) { return fake.New(nil), nil }
	failBuild := func(*config.Endpoint) (provider.Provider, error) { return nil, errors.New("no key") }
	entry := func(e, m string) *config.Group {
		return &config.Group{Endpoints: []config.GroupEntry{{Endpoint: e, Model: m}}}
	}
	tests := []struct {
		name  string
		group *config.Group
		build func(*config.Endpoint) (provider.Provider, error)
	}{
		{"nil group", nil, okBuild},
		{"no endpoints", &config.Group{}, okBuild},
		{"undefined endpoint", entry("x", "m"), okBuild},
		{"undeclared model", entry("e", "x"), okBuild},
		{"build fails", entry("e", "m"), failBuild},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewGroup("g", tt.group, cfg, tt.build); err == nil {
				t.Error("NewGroup() error = nil, want an error")
			}
		})
	}
}

func TestNewGroup_buildsEachEndpointOnce(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Endpoints: map[string]*config.Endpoint{
		"e": {Models: map[string]config.Model{"m1": {}, "m2": {}}},
	}}
	g := &config.Group{Endpoints: []config.GroupEntry{{Endpoint: "e", Model: "m1"}, {Endpoint: "e", Model: "m2"}}}
	builds := 0
	group, err := NewGroup("g", g, cfg, func(*config.Endpoint) (provider.Provider, error) {
		builds++
		return fake.New(nil), nil
	})
	if err != nil || builds != 1 || len(group.Health()) != 2 || group.maxTries != 2 {
		t.Errorf("NewGroup() = %v, builds %d; want 1 build, 2 pairs, max tries defaulting to 2", err, builds)
	}
}

func TestGroup_OnFailover(t *testing.T) {
	t.Parallel()
	g, _ := newGroup(t, config.Failover{MaxTries: 5},
		spec{name: "a", script: fake.AlwaysRetryable(503)},
		spec{name: "b", script: fake.AlwaysRetryable(503)},
		spec{name: "c", script: fake.Script{{Answer: "ok", Repeat: true}}})
	var moves []string
	g.OnFailover = func(req provider.Request, from, to Served) {
		moves = append(moves, from.Endpoint+">"+to.Endpoint+":"+req.Parts[0].Text)
	}
	if _, served, err := g.Complete(context.Background(), textReq); err != nil || served.Endpoint != "c" {
		t.Fatalf("Complete = %v, %v", served, err)
	}
	if want := []string{"a>b:instructions", "b>c:instructions"}; !slices.Equal(moves, want) {
		t.Errorf("failovers = %v, want %v", moves, want)
	}
	moves = nil
	if _, _, err := g.Complete(context.Background(), textReq); err != nil {
		t.Fatal(err)
	}
	// a and b are marked down now: c is tried first and answers, no failover.
	if len(moves) != 0 {
		t.Errorf("failovers on a first-try answer: %v", moves)
	}
}

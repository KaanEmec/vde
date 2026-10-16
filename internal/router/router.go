package router

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

var (
	// ErrGroupUnavailable: every eligible pair failed retryably, or the tries or the
	// group's max_total_time ran out. It wraps the last pair's error.
	ErrGroupUnavailable = errors.New("router: endpoint group unavailable")
	// ErrPermanent: a pair failed permanently (refusal, bad request) and the group does
	// not fail over on permanent errors, or no pair accepts the request's media types.
	ErrPermanent = errors.New("router: permanent failure")
)

const (
	// backoff is the mean wait between tries; each wait is jittered ±50%.
	backoff = 250 * time.Millisecond
	// maxRetryAfter is the longest Retry-After the router waits out on the same pair.
	maxRetryAfter = 10 * time.Second
)

// Served names the pair that answered, for metrics and logs only.
type Served struct {
	Endpoint string
	Model    string
}

func (s Served) String() string { return s.Endpoint + "/" + s.Model }

type pair struct {
	Served
	provider provider.Provider
	accepts  []string
	maxOut   int           // output-token limit for requests that set none
	timeout  time.Duration // per try; 0 for none
}

// Group serves one role from an ordered list of endpoint-model pairs. It is safe for
// concurrent use.
type Group struct {
	name                string
	pairs               []pair
	maxTries            int
	maxTotalTime        time.Duration
	failoverOnPermanent bool

	backoff time.Duration    // mean wait between tries
	now     func() time.Time // the clock for health marking

	// OnFailover, when set, is called each time a call moves on from one pair to
	// another, with the caller's request and both pairs; for metrics.
	// Set it before the first Complete; it must not block.
	OnFailover func(req provider.Request, from, to Served)

	mu     sync.Mutex
	health []health // per pair, guarded by mu
}

// NewGroup builds the group name from its configuration. build returns the provider for
// an endpoint; it is called once per endpoint the group uses.
func NewGroup(name string, g *config.Group, cfg *config.Config,
	build func(*config.Endpoint) (provider.Provider, error),
) (*Group, error) {
	if g == nil || len(g.Endpoints) == 0 {
		return nil, fmt.Errorf("router: group %q has no endpoints", name)
	}
	built := map[string]provider.Provider{}
	pairs := make([]pair, 0, len(g.Endpoints))
	for _, entry := range g.Endpoints {
		e := cfg.Endpoints[entry.Endpoint]
		if e == nil {
			return nil, fmt.Errorf("router: group %q: endpoint %q is not defined", name, entry.Endpoint)
		}
		m, ok := e.Models[entry.Model]
		if !ok {
			return nil, fmt.Errorf("router: group %q: model %q is not declared on endpoint %q", name, entry.Model, entry.Endpoint)
		}
		p, ok := built[entry.Endpoint]
		if !ok {
			var err error
			if p, err = build(e); err != nil {
				return nil, fmt.Errorf("router: group %q: endpoint %q: %w", name, entry.Endpoint, err)
			}
			built[entry.Endpoint] = p
		}
		pairs = append(pairs, pair{
			Served:   Served{Endpoint: entry.Endpoint, Model: entry.Model},
			provider: p,
			accepts:  slices.Clone(m.Accepts),
			maxOut:   m.OutputTokens(),
			timeout:  e.Timeout,
		})
	}
	maxTries := g.Failover.MaxTries
	if maxTries <= 0 {
		maxTries = len(pairs)
	}
	return &Group{
		name:                name,
		pairs:               pairs,
		maxTries:            maxTries,
		maxTotalTime:        g.Failover.MaxTotalTime,
		failoverOnPermanent: g.Failover.FailoverOnPermanent,
		backoff:             backoff,
		now:                 time.Now,
		health:              make([]health, len(pairs)),
	}, nil
}

// Name returns the group's configured name.
func (g *Group) Name() string { return g.name }

// Complete sends req to the group's pairs in order, healthy pairs first, skipping pairs
// that cannot read the media types of req's parts, until one answers. req.Model, and
// req.MaxTokens when it is 0, are set per pair. The returned errors are described in the package documentation.
func (g *Group) Complete(ctx context.Context, req provider.Request) (provider.Response, Served, error) {
	order, err := g.order(req)
	if err != nil {
		return provider.Response{}, Served{}, err
	}
	budget := ctx
	if g.maxTotalTime > 0 {
		var cancel context.CancelFunc
		budget, cancel = context.WithTimeout(ctx, g.maxTotalTime)
		defer cancel()
	}

	var last error
	wait := time.Duration(0)
	retried := false // the current pair was retried after its Retry-After
	prev := -1       // the pair tried last
	for i, tries := 0, 0; i < len(order) && tries < g.maxTries; tries++ {
		if !sleep(budget, wait) {
			break
		}
		idx := order[i]
		p := &g.pairs[idx]
		if prev >= 0 && prev != idx && g.OnFailover != nil {
			g.OnFailover(req, g.pairs[prev].Served, p.Served)
		}
		prev = idx
		resp, err := g.try(budget, p, req)
		if err == nil {
			g.markUp(idx)
			return resp, p.Served, nil
		}
		last = fmt.Errorf("%s: %w", p.Served, err)
		if budget.Err() != nil {
			break // cut short by the caller or the group's budget, not the pair's fault
		}
		if provider.Classify(err) == provider.Permanent {
			if !g.failoverOnPermanent {
				return provider.Response{}, Served{}, fmt.Errorf("%w: group %q: %w", ErrPermanent, g.name, last)
			}
			i, retried, wait = i+1, false, g.jitter()
			continue
		}
		ra := retryAfter(err)
		g.markDown(idx, ra)
		if ra > 0 && ra <= maxRetryAfter && !retried && fits(budget, ra) {
			retried, wait = true, ra
			continue
		}
		i, retried, wait = i+1, false, g.jitter()
	}
	if err := ctx.Err(); err != nil {
		return provider.Response{}, Served{}, err // the caller's deadline or cancellation, unchanged
	}
	if last == nil {
		last = budget.Err()
	}
	return provider.Response{}, Served{}, fmt.Errorf("%w: group %q: %w", ErrGroupUnavailable, g.name, last)
}

// try makes one call to p within the endpoint's timeout.
func (g *Group) try(ctx context.Context, p *pair, req provider.Request) (provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return provider.Response{}, err // never call a provider after the context is done
	}
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	req.Model = p.Model
	if req.MaxTokens == 0 {
		req.MaxTokens = p.maxOut
	}
	return p.provider.Complete(ctx, req)
}

// order returns the indexes of the pairs that can read req, healthy first, each part in
// configured order.
func (g *Group) order(req provider.Request) ([]int, error) {
	var types []string
	for _, part := range req.Parts {
		for _, b := range []*provider.Blob{part.Image, part.Document} {
			if b != nil && !slices.Contains(types, b.MediaType) {
				types = append(types, b.MediaType)
			}
		}
	}
	now := g.now()
	var healthy, down []int
	g.mu.Lock()
	for i, p := range g.pairs {
		if !acceptsAll(p.accepts, types) {
			continue
		}
		if g.health[i].isDown(now) {
			down = append(down, i)
		} else {
			healthy = append(healthy, i)
		}
	}
	g.mu.Unlock()
	if len(healthy)+len(down) == 0 {
		return nil, fmt.Errorf("%w: group %q: no endpoint-model pair accepts %v", ErrPermanent, g.name, types)
	}
	return append(healthy, down...), nil
}

func acceptsAll(accepts, types []string) bool {
	for _, t := range types {
		if !slices.Contains(accepts, t) {
			return false
		}
	}
	return true
}

func (g *Group) jitter() time.Duration {
	if g.backoff <= 0 {
		return 0
	}
	return g.backoff/2 + rand.N(g.backoff)
}

// sleep waits d, reporting false when ctx is done first or already.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// fits reports whether waiting d still leaves time before ctx's deadline.
func fits(ctx context.Context, d time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > d
}

func retryAfter(err error) time.Duration {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return pe.RetryAfter
	}
	return 0
}

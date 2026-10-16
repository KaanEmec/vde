package router

import "time"

// Cooldown after a retryable failure: baseCooldown, doubling per consecutive failed
// window up to maxCooldown, reset by a success.
const (
	baseCooldown = 30 * time.Second
	maxCooldown  = 5 * time.Minute
)

type health struct {
	downUntil time.Time
	failures  int // consecutive failed windows
}

func (h health) isDown(now time.Time) bool { return now.Before(h.downUntil) }

// PairHealth is one pair's state, for /healthz and /metrics.
type PairHealth struct {
	Served
	DownUntil time.Time // zero or past when the pair is healthy
	Failures  int       // consecutive failed windows, 0 after a success
}

// markDown deprioritizes pair i after a retryable failure. A pair already marked down is
// left alone, so concurrent failures within one window count once.
func (g *Group) markDown(i int, retryAfter time.Duration) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	h := &g.health[i]
	if h.isDown(now) {
		return
	}
	h.failures++
	d := baseCooldown
	for range h.failures - 1 {
		if d *= 2; d >= maxCooldown {
			break
		}
	}
	d = max(d, retryAfter)
	h.downUntil = now.Add(min(d, maxCooldown))
}

// markUp clears pair i's marking after a success.
func (g *Group) markUp(i int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.health[i] = health{}
}

// Healthy reports whether any pair of the group is not marked down.
func (g *Group) Healthy() bool {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, h := range g.health {
		if !h.isDown(now) {
			return true
		}
	}
	return false
}

// Health returns every pair's state in configured order.
func (g *Group) Health() []PairHealth {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]PairHealth, len(g.pairs))
	for i, p := range g.pairs {
		out[i] = PairHealth{Served: p.Served, DownUntil: g.health[i].downUntil, Failures: g.health[i].failures}
	}
	return out
}

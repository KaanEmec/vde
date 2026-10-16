package router

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// clock is a manual clock; tests using it call Complete sequentially.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestGroup_healthMarking(t *testing.T) {
	t.Parallel()
	c := &clock{t: time.Unix(1_000_000, 0)}
	primary := fake.Script{
		fake.Fail(provider.Retryable, 503), // marks down for 30s
		fake.Answer("ok"),                  // after the window: clears the marking
	}
	g, fakes := newGroup(t, config.Failover{},
		spec{name: "a", script: primary}, spec{name: "b", script: fake.Script{{Answer: "ok", Repeat: true}}})
	g.now = c.now
	served := func() string {
		t.Helper()
		_, s, err := g.Complete(context.Background(), textReq)
		if err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		return s.Endpoint
	}

	if got := served(); got != "b" {
		t.Fatalf("first call served by %s, want b after a failed", got)
	}
	h := g.Health()
	if !h[0].DownUntil.Equal(c.t.Add(baseCooldown)) || h[0].Failures != 1 || !h[1].DownUntil.IsZero() {
		t.Errorf("Health() = %+v, want a down for %s", h, baseCooldown)
	}
	if got := served(); got != "b" {
		t.Errorf("call within the window served by %s, want b preferred", got)
	}
	if got := callCounts(fakes); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("calls per pair = %v, want [1 2]: a is not tried while b answers", got)
	}

	c.advance(baseCooldown)
	if got := served(); got != "a" {
		t.Errorf("call after the window served by %s, want a: order restored", got)
	}
	if h := g.Health(); !h[0].DownUntil.IsZero() || h[0].Failures != 0 {
		t.Errorf("Health() after success = %+v, want a cleared", h[0])
	}
}

func TestGroup_markedDownPairStillTried(t *testing.T) {
	t.Parallel()
	c := &clock{t: time.Unix(1_000_000, 0)}
	g, fakes := newGroup(t, config.Failover{},
		spec{name: "a", script: fake.Script{fake.Fail(provider.Retryable, 503), fake.Answer("ok")}},
		spec{name: "b", script: fake.AlwaysRetryable(503)})
	g.now = c.now
	if _, _, err := g.Complete(context.Background(), textReq); err == nil {
		t.Fatal("first Complete() error = nil, want both pairs failing")
	}
	if g.Healthy() {
		t.Error("Healthy() = true with every pair marked down")
	}
	_, s, err := g.Complete(context.Background(), textReq)
	if err != nil || s.Endpoint != "a" {
		t.Fatalf("Complete() = %s, %v; want a served as a marked-down fallback", s, err)
	}
	if got := callCounts(fakes); !slices.Equal(got, []int{2, 1}) {
		t.Errorf("calls per pair = %v, want [2 1]: marked-down pairs tried in configured order", got)
	}
	if !g.Healthy() {
		t.Error("Healthy() = false after a success")
	}
}

func TestGroup_markDownCooldown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		failures   int // consecutive failed windows before this one
		retryAfter time.Duration
		want       time.Duration
	}{
		{"first failure", 0, 0, 30 * time.Second},
		{"second window doubles", 1, 0, time.Minute},
		{"third window doubles again", 2, 0, 2 * time.Minute},
		{"capped", 10, 0, maxCooldown},
		{"retry-after lengthens", 0, 90 * time.Second, 90 * time.Second},
		{"retry-after capped", 0, time.Hour, maxCooldown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &clock{t: time.Unix(1_000_000, 0)}
			g, _ := newGroup(t, config.Failover{}, spec{name: "a"})
			g.now = c.now
			g.health[0].failures = tt.failures
			g.markDown(0, tt.retryAfter)
			if got := g.Health()[0].DownUntil.Sub(c.t); got != tt.want {
				t.Errorf("cooldown = %s, want %s", got, tt.want)
			}
		})
	}
}

// With the primary timing out, a sequence of jobs pays the primary's timeout once per
// window, not once per job.
func TestGroup_primaryTimeoutOncePerWindow(t *testing.T) {
	t.Parallel()
	c := &clock{t: time.Unix(1_000_000, 0)}
	g, fakes := newGroup(t, config.Failover{},
		spec{name: "a", timeout: 10 * time.Millisecond, script: fake.Script{{Delay: time.Hour, Answer: "late", Repeat: true}}},
		spec{name: "b", script: fake.Script{{Answer: "ok", Repeat: true}}})
	g.now = c.now
	run := func(jobs int) {
		t.Helper()
		for range jobs {
			if _, s, err := g.Complete(context.Background(), textReq); err != nil || s.Endpoint != "b" {
				t.Fatalf("Complete() = %s, %v; want b", s, err)
			}
		}
	}

	run(5)
	if n := len(fakes[0].Calls()); n != 1 {
		t.Errorf("primary called %d times in the first window, want 1", n)
	}
	c.advance(baseCooldown) // the window ends; the next failure doubles it to 60s
	run(5)
	if n := len(fakes[0].Calls()); n != 2 {
		t.Errorf("primary called %d times after two windows, want 2", n)
	}
	c.advance(baseCooldown) // still inside the doubled window
	run(5)
	if n := len(fakes[0].Calls()); n != 2 {
		t.Errorf("primary called %d times inside the doubled window, want 2", n)
	}
}

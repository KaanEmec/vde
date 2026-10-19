package api

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

// inProgressRetryAfter is the Retry-After of a submission refused for the in-progress
// ceiling: a job slot frees when a job finishes, which takes seconds.
const inProgressRetryAfter = 5 * time.Second

// Bucket kinds, one bucket of each per application.
type bucketKind int

const (
	bucketRequests   bucketKind = iota + 1 // every /v1 request (limit)
	bucketSubmits                          // POST /v1/jobs and /v1/extract, after the part rules and an idempotent replay, before the document work
	bucketAdmin                            // every admin API request, keyed by admin token id
	bucketAuthFailed                       // auth.failed audit rows of the handler, one bucket (app "")
	bucketHealthDB                         // database checks of /healthz, one bucket (app "")
)

// The auth.failed audit rows a handler writes: anyone can provoke a 401 without a token,
// so the rows are capped at authFailedAuditPerSecond with a burst of authFailedAuditBurst;
// a refusal over it is counted in vde_audit_auth_failed_dropped_total instead, and still
// answers 401.
const (
	authFailedAuditPerSecond = 10
	authFailedAuditBurst     = 30
)

type bucketKey struct {
	app  string
	kind bucketKind
}

// bucket is a token bucket holding up to one minute of allowance, refilled continuously
// at perMinute/60 tokens per second.
type bucket struct {
	tokens float64
	last   time.Time
}

// rateLimiter holds the in-memory limits of one server (server.limits): token buckets per application
// (ADR 0003: one process; several replicas each apply their own limits unless rate.shared
// moves the request and submit limits into the store, takeRate), and the locks
// that serialize submissions sharing an idempotency key.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[bucketKey]*bucket
	keys    map[string]*keyLock
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[bucketKey]*bucket{}, keys: map[string]*keyLock{}}
}

// take removes one token from the application's bucket of kind, which allows perMinute
// requests per minute with a burst of perMinute. When the bucket is empty it returns
// false and the wait until a token is available, in whole seconds, at least 1.
// perMinute <= 0 means no limit.
func (l *rateLimiter) take(app string, kind bucketKind, perMinute int, now time.Time) (time.Duration, bool) {
	if perMinute <= 0 {
		return 0, true
	}
	return l.takeBucket(bucketKey{app, kind}, float64(perMinute), float64(perMinute)/60, now)
}

// allowAuthFailedAudit reports whether an auth.failed audit row may be written now
// (authFailedAuditPerSecond, burst authFailedAuditBurst, for the whole handler).
func (l *rateLimiter) allowAuthFailedAudit(now time.Time) bool {
	_, ok := l.takeBucket(bucketKey{"", bucketAuthFailed}, authFailedAuditBurst, authFailedAuditPerSecond, now)
	return ok
}

// takeBucket removes one token from the bucket of key, which holds up to capacity tokens
// and refills at perSecond; see take.
func (l *rateLimiter) takeBucket(key bucketKey, capacity, perSecond float64, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 { // a clock going back refills nothing
		b.tokens += elapsed.Seconds() * perSecond
		b.last = now
	}
	b.tokens = min(b.tokens, capacity) // also when the limit was lowered
	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	wait := time.Duration((1 - b.tokens) / perSecond * float64(time.Second))
	return wholeSeconds(wait), false
}

// takeRate applies the application's limit of kind: the in-memory bucket of this process
// (take), or with rate.shared: true the job store's fixed-window counter of the current
// UTC minute, which every replica shares (one UPSERT per request). A refused request
// waits for the next minute. A store failure admits the request (the quota, charged in
// the store, still holds) and is logged. perMinute <= 0 means no limit.
func (s *server) takeRate(ctx context.Context, app string, kind bucketKind, perMinute int, now time.Time) (time.Duration, bool) {
	storeKind, shareable := map[bucketKind]string{bucketRequests: job.RateRequests, bucketSubmits: job.RateSubmits}[kind]
	if perMinute <= 0 || !shareable || !s.deps.Config.Rate.Shared {
		return s.limits.take(app, kind, perMinute, now)
	}
	start := now.UTC().Truncate(time.Minute)
	_, err := s.deps.Store.IncrementRate(ctx, app, storeKind, start, perMinute)
	switch {
	case err == nil:
		return 0, true
	case errors.Is(err, job.ErrRateLimited):
		return wholeSeconds(start.Add(time.Minute).Sub(now)), false
	}
	s.log.LogAttrs(ctx, slog.LevelWarn, "shared rate counter not written; request admitted",
		slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
	return 0, true
}

// keyLock serializes the submissions of one application sharing an idempotency key, so
// that the second sees the first's job and replays it instead of racing it to the
// unique index (which still guards against other processes).
type keyLock struct {
	mu   sync.Mutex
	refs int
}

// lockKey locks the application's idempotency key and returns the unlock function.
func (l *rateLimiter) lockKey(app, key string) func() {
	name := app + "\x00" + key
	l.mu.Lock()
	k, ok := l.keys[name]
	if !ok {
		k = &keyLock{}
		l.keys[name] = k
	}
	k.refs++
	l.mu.Unlock()
	k.mu.Lock()
	return func() {
		k.mu.Unlock()
		l.mu.Lock()
		if k.refs--; k.refs == 0 {
			delete(l.keys, name)
		}
		l.mu.Unlock()
	}
}

package api

import (
	"sync"
	"time"
)

// inProgressRetryAfter is the Retry-After of a submission refused for the in-progress
// ceiling: a job slot frees when a job finishes, which takes seconds.
const inProgressRetryAfter = 5 * time.Second

// Bucket kinds, one bucket of each per application.
type bucketKind int

const (
	bucketRequests bucketKind = iota + 1 // every /v1 request (limit)
	bucketSubmits                        // POST /v1/jobs, after validation and idempotency
	bucketAdmin                          // every admin API request, keyed by admin token id
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
// (ADR 0003: one process; several replicas each apply their own limits), and the locks
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
	capacity := float64(perMinute)
	perSecond := capacity / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	key := bucketKey{app, kind}
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

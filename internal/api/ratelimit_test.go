package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/provider/fake"
)

func TestBucket(t *testing.T) {
	t.Parallel()
	l := newRateLimiter()
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		if _, ok := l.take("a", bucketRequests, 3, t0); !ok {
			t.Fatalf("token %d refused within the burst", i+1)
		}
	}
	if wait, ok := l.take("a", bucketRequests, 3, t0); ok || wait != 20*time.Second {
		t.Errorf("empty bucket: ok %v wait %s, want refused and 20s", ok, wait)
	}
	if wait, ok := l.take("a", bucketRequests, 3, t0.Add(19500*time.Millisecond)); ok || wait != time.Second {
		t.Errorf("0.5s before a token: ok %v wait %s, want refused and 1s (rounded up)", ok, wait)
	}
	if _, ok := l.take("a", bucketRequests, 3, t0.Add(20*time.Second)); !ok {
		t.Error("no token after the refill time")
	}
	if _, ok := l.take("a", bucketSubmits, 3, t0); !ok {
		t.Error("the submit bucket shares the request bucket's tokens")
	}
	if _, ok := l.take("b", bucketRequests, 3, t0); !ok {
		t.Error("applications share a bucket")
	}
	if wait, ok := l.take("a", bucketRequests, 3, t0); ok || wait < time.Second {
		t.Errorf("a clock going back refilled the bucket: ok %v wait %s", ok, wait)
	}
	for range 100 {
		if _, ok := l.take("c", bucketRequests, 0, t0); !ok {
			t.Fatal("0 per minute is no limit")
		}
	}
	l.take("d", bucketRequests, 7, t0)
	for range 6 {
		l.take("d", bucketRequests, 7, t0)
	}
	if wait, _ := l.take("d", bucketRequests, 7, t0); wait != 9*time.Second {
		t.Errorf("wait = %s, want 60/7 s rounded up to 9s", wait)
	}
}

func TestRequestRateLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	rg := newSubmitRig(t, submitRigOptions{now: now, app: func(a *job.Application) { a.RequestsPerMinute = 5 }})
	for i := range 5 {
		if w := rg.get("/v1/openapi.yaml"); w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i+1, w.Code)
		}
	}
	w := rg.get("/v1/openapi.yaml")
	if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeRateLimited {
		t.Fatalf("status = %d, want 429 rate_limited", w.Code)
	}
	if w.Header().Get("Retry-After") != "12" || w.Header().Get(headerQuotaRemaining) != "100" {
		t.Errorf("Retry-After %q, X-Quota-Remaining %q; want 12 and 100 (above zero)",
			w.Header().Get("Retry-After"), w.Header().Get(headerQuotaRemaining))
	}
	// A submission is a request too.
	if w := rg.submit(t, validParts(), nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("submission while limited: %d, want 429", w.Code)
	}
	rg.clock.Set(now.Add(12 * time.Second))
	if w := rg.get("/v1/openapi.yaml"); w.Code != http.StatusOK {
		t.Errorf("after Retry-After: %d, want 200", w.Code)
	}
	if rg.quotaUsed(t) != 0 {
		t.Error("a rate-limited submission was charged")
	}
}

func TestSubmitRateLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	rg := newSubmitRig(t, submitRigOptions{now: now, app: func(a *job.Application) { a.SubmitsPerMinute = 2 }})
	for range 2 {
		decodeJob(t, rg.submit(t, validParts(), nil))
	}
	// An invalid request is refused for what it is, without using a submit token.
	if w := rg.submit(t, withoutPart(validParts(), partDocument), nil); w.Code != http.StatusBadRequest {
		t.Errorf("invalid submission: %d, want 400", w.Code)
	}
	w := rg.submit(t, validParts(), nil)
	if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeRateLimited {
		t.Fatalf("status = %d, want 429 rate_limited", w.Code)
	}
	if w.Header().Get("Retry-After") != "30" || w.Header().Get(headerQuotaRemaining) != "98" {
		t.Errorf("Retry-After %q, X-Quota-Remaining %q; want 30 and 98", w.Header().Get("Retry-After"),
			w.Header().Get(headerQuotaRemaining))
	}
	if g := rg.get("/v1/openapi.yaml"); g.Code != http.StatusOK {
		t.Errorf("a read while submissions are limited: %d, want 200", g.Code)
	}
	if rg.quotaUsed(t) != 2 || rg.blobJobs(t) != 2 {
		t.Errorf("quota %d, blob jobs %d; want 2 and 2", rg.quotaUsed(t), rg.blobJobs(t))
	}
	rg.clock.Set(now.Add(30 * time.Second))
	decodeJob(t, rg.submit(t, validParts(), nil))
}

// TestInProgressCeiling holds jobs running in a blocked fake provider: at the ceiling
// the next submission is 429 rate_limited with Retry-After: 5, uncharged.
func TestInProgressCeiling(t *testing.T) {
	t.Parallel()
	blocked := fake.Script{{Answer: submitCandidate, Delay: time.Hour, Repeat: true}}
	rg := newSubmitRig(t, submitRigOptions{pool: true, ext: blocked, app: func(a *job.Application) { a.MaxInProgress = 2 }})
	for range 2 {
		decodeJob(t, rg.submit(t, validParts(), nil))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		n, err := rg.db.CountRunning(t.Context(), testApp)
		if err != nil {
			t.Fatal(err)
		}
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs running, want 2", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	w := rg.submit(t, validParts(), nil)
	if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeRateLimited {
		t.Fatalf("status = %d, want 429 rate_limited", w.Code)
	}
	if w.Header().Get("Retry-After") != "5" || w.Header().Get(headerQuotaRemaining) != "98" {
		t.Errorf("Retry-After %q, X-Quota-Remaining %q; want 5 and 98", w.Header().Get("Retry-After"),
			w.Header().Get(headerQuotaRemaining))
	}
	if rg.quotaUsed(t) != 2 || rg.blobJobs(t) != 2 {
		t.Errorf("quota %d, blob jobs %d; want 2 and 2", rg.quotaUsed(t), rg.blobJobs(t))
	}
	for len(rg.ext.Calls()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond) // the held jobs reach the extractor
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(rg.ext.Calls()); n != 2 {
		t.Errorf("extractor calls = %d, want the 2 held jobs only", n)
	}
	if n := len(rg.judge.Calls()); n != 0 {
		t.Errorf("judge calls = %d, want 0", n)
	}
}

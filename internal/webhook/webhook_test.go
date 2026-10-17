package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/webhook"
)

var secret = []byte("0123456789abcdef-test-secret")

// events collects the audit events a dispatcher records.
type events struct {
	mu   sync.Mutex
	list []job.AuditEvent
}

func (e *events) AppendAudit(_ context.Context, ev job.AuditEvent) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, ev)
	return int64(len(e.list)), nil
}

func (e *events) wait(t *testing.T, n int) []job.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		got := append([]job.AuditEvent(nil), e.list...)
		e.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fewer than %d audit events", n)
	return nil
}

// receiver is an httptest webhook receiver answering statuses in order (the last one
// repeats) and verifying each signature as a receiver would.
type receiver struct {
	mu       sync.Mutex
	statuses []int
	bodies   [][]byte
	verified []error
	headers  []http.Header
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.bodies = append(rc.bodies, body)
	rc.headers = append(rc.headers, r.Header.Clone())
	rc.verified = append(rc.verified, webhook.Verify(secret, r.Header.Get(webhook.HeaderSignature), body, time.Now(), 5*time.Minute))
	status := rc.statuses[min(len(rc.bodies)-1, len(rc.statuses)-1)]
	w.WriteHeader(status)
}

func finishedJob() job.Job {
	return job.Job{
		ID: "j_01TEST", ApplicationID: "bookings", Status: job.StatusAccepted, ServiceVersion: "vde-0.3.0+abc",
		FinishedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		Result:     json.RawMessage(`{"name":"SENTINEL"}`), Feedback: json.RawMessage(`[{"message":"SENTINEL"}]`),
	}
}

func newDispatcher(t *testing.T, url string, ev *events, backoff []time.Duration) *webhook.Dispatcher {
	t.Helper()
	d := webhook.New(map[string]webhook.Target{"bookings": {URL: url, Secret: secret}}, webhook.Options{
		Backoff: backoff, AttemptTimeout: 2 * time.Second, Audit: audit.New(ev, audit.Options{}),
	})
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	return d
}

func TestFinishedJobTriggersOneSignedDelivery(t *testing.T) {
	t.Parallel()
	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	ev := &events{}
	d := newDispatcher(t, srv.URL, ev, nil)
	d.JobFinished(finishedJob())
	d.JobFinished(job.Job{ID: "j_other", ApplicationID: "no-webhook"}) // no target: nothing sent

	got := ev.wait(t, 1)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.bodies) != 1 {
		t.Fatalf("%d deliveries, want 1", len(rc.bodies))
	}
	if rc.verified[0] != nil {
		t.Errorf("signature not verified: %v", rc.verified[0])
	}
	if h := rc.headers[0]; h.Get(webhook.HeaderEvent) != "job.finished" || h.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", h)
	}
	var p map[string]any
	if err := json.Unmarshal(rc.bodies[0], &p); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"event": "job.finished", "job_id": "j_01TEST", "application_id": "bookings", "status": "accepted",
		"service_version": "vde-0.3.0+abc", "finished_at": "2026-10-16T12:00:00Z",
	}
	if len(p) != len(want) {
		t.Errorf("payload = %v, want %v", p, want)
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("payload[%s] = %v, want %v", k, p[k], v)
		}
	}
	if strings.Contains(string(rc.bodies[0]), "SENTINEL") {
		t.Error("payload carries content")
	}
	if got[0].Kind != string(audit.WebhookDeliveredKind) || got[0].Actor != audit.ActorWebhook || got[0].JobID != "j_01TEST" {
		t.Errorf("audit event = %+v", got[0])
	}
}

func TestRetriesWithBackoffUntilDelivered(t *testing.T) {
	t.Parallel()
	rc := &receiver{statuses: []int{http.StatusServiceUnavailable, http.StatusFound, http.StatusOK}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	ev := &events{}
	d := newDispatcher(t, srv.URL, ev, []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	d.JobFinished(finishedJob())

	got := ev.wait(t, 3)
	var kinds []string
	for _, e := range got {
		details, err := audit.Decode(e)
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, e.Kind)
		if f, ok := details.(audit.WebhookFailed); ok && (f.Final || f.Reason != audit.WebhookStatus) {
			t.Errorf("failed attempt = %+v, want a non-final status failure", f)
		}
	}
	// A redirect is a failure: it is not followed.
	if strings.Join(kinds, ",") != "webhook.failed,webhook.failed,webhook.delivered" {
		t.Errorf("audit kinds = %v", kinds)
	}
}

func TestGivesUpAfterTheLastAttempt(t *testing.T) {
	t.Parallel()
	rc := &receiver{statuses: []int{http.StatusInternalServerError}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	ev := &events{}
	d := newDispatcher(t, srv.URL, ev, []time.Duration{time.Millisecond})
	d.JobFinished(finishedJob())
	got := ev.wait(t, 2)
	time.Sleep(50 * time.Millisecond)
	rc.mu.Lock()
	n := len(rc.bodies)
	rc.mu.Unlock()
	if n != 2 {
		t.Errorf("%d attempts, want 2", n)
	}
	last, err := audit.Decode(got[1])
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := last.(audit.WebhookFailed); !ok || !f.Final || f.Attempt != 2 || f.StatusCode != 500 {
		t.Errorf("last event = %+v, want the final failure of attempt 2", last)
	}
}

func TestStopCancelsPendingRetries(t *testing.T) {
	t.Parallel()
	rc := &receiver{statuses: []int{http.StatusBadGateway}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	ev := &events{}
	d := newDispatcher(t, srv.URL, ev, []time.Duration{time.Hour})
	d.JobFinished(finishedJob())
	ev.wait(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("Stop = %v, want the hour-long backoff cancelled", err)
	}
	d.JobFinished(finishedJob()) // after Stop: dropped
}

func TestVerifyRefusesTamperedAndStale(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"event":"job.finished"}`)
	sig := webhook.Sign(secret, now, body)
	tests := []struct {
		name   string
		secret []byte
		header string
		body   []byte
		now    time.Time
		ok     bool
	}{
		{"valid", secret, sig, body, now, true},
		{"tampered body", secret, sig, []byte(`{"event":"job.finishe"}`), now, false},
		{"wrong secret", []byte("another-secret-of-length"), sig, body, now, false},
		{"stale", secret, sig, body, now.Add(10 * time.Minute), false},
		{"future", secret, sig, body, now.Add(-10 * time.Minute), false},
		{"missing v1", secret, "t=1800000000", body, now, false},
		{"empty", secret, "", body, now, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := webhook.Verify(tt.secret, tt.header, tt.body, tt.now, 5*time.Minute)
			if (err == nil) != tt.ok {
				t.Errorf("Verify = %v, want ok %v", err, tt.ok)
			}
		})
	}
	if !strings.HasPrefix(sig, "t=1800000000,v1=") || len(sig) != len("t=1800000000,v1=")+64 {
		t.Errorf("signature = %q", sig)
	}
}

// TestExtractedStatusPassesThrough: a job ended extracted (judge policy never, ADR 0009)
// is notified with its status as is; the notice gains no field.
func TestExtractedStatusPassesThrough(t *testing.T) {
	t.Parallel()
	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	ev := &events{}
	d := newDispatcher(t, srv.URL, ev, nil)
	j := finishedJob()
	j.Status, j.Verification, j.JudgePolicy = job.StatusExtracted, job.VerificationNone, "never"
	d.JobFinished(j)
	ev.wait(t, 1)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var p map[string]any
	if len(rc.bodies) != 1 || json.Unmarshal(rc.bodies[0], &p) != nil {
		t.Fatalf("deliveries = %d", len(rc.bodies))
	}
	if p["status"] != "extracted" || len(p) != 6 {
		t.Errorf("payload = %v, want status extracted and the six fields", p)
	}
}

package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/worker"
)

var _ worker.Observer = (*metrics.Metrics)(nil)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestSeries(t *testing.T) {
	t.Parallel()
	m := metrics.New()
	m.JobStarted("billing")
	m.JobFinished("billing", job.StatusAccepted, 2, 3*time.Second)
	m.JobFinished("billing", job.StatusAccepted, 1, time.Second)
	m.JobFinished("billing", job.StatusFailed, 3, 90*time.Second)
	m.ObserveHTTP("POST /v1/jobs", 202)
	m.ObserveHTTP("", 404)
	m.ObserveProviderCall(metrics.ProviderCall{Role: "extractor", Group: "main", Endpoint: "primary", Model: "model-a",
		Outcome: "ok", Duration: 1200 * time.Millisecond, InputTokens: 1500, OutputTokens: 200})
	m.ObserveProviderCall(metrics.ProviderCall{Role: "extractor", Group: "main", Endpoint: "primary", Model: "model-a",
		Outcome: "ok", Duration: time.Second, InputTokens: 500, OutputTokens: 100})
	m.ObserveProviderCall(metrics.ProviderCall{Role: "judge", Group: "second", Endpoint: "review", Model: "model-b",
		Outcome: "retryable", Duration: 300 * time.Millisecond})
	m.WatchEndpoints("host-1", func() []metrics.Endpoint {
		return []metrics.Endpoint{{Group: "main", Endpoint: "primary", Model: "model-a", Up: true},
			{Group: "second", Endpoint: "review", Model: "model-b"}}
	})
	m.ObserveFailover("extractor", "primary", "fallback")
	m.SetQuotaRemaining("billing", 41)
	m.SetJobsInProgress("billing", 3)
	m.AddSwept(5)
	m.AddSwept(0)
	m.AddSwept(-2)

	out := scrape(t, m.Handler())
	for _, want := range []string{
		`vde_jobs_total{app="billing",status="accepted"} 2`,
		`vde_jobs_total{app="billing",status="failed"} 1`,
		`vde_job_attempts_count 3`,
		`vde_job_attempts_bucket{le="2"} 2`,
		`vde_job_duration_seconds_count{status="accepted"} 2`,
		`vde_job_duration_seconds_count{status="failed"} 1`,
		`vde_provider_calls_total{endpoint="primary",group="main",model="model-a",outcome="ok",role="extractor"} 2`,
		`vde_provider_calls_total{endpoint="review",group="second",model="model-b",outcome="retryable",role="judge"} 1`,
		`vde_provider_latency_seconds_count{endpoint="primary",group="main",role="extractor"} 2`,
		`vde_provider_tokens_total{endpoint="primary",group="main",kind="input",model="model-a",role="extractor"} 2000`,
		`vde_provider_tokens_total{endpoint="primary",group="main",kind="output",model="model-a",role="extractor"} 300`,
		`vde_endpoint_up{endpoint="primary",group="main",instance="host-1",model="model-a"} 1`,
		`vde_endpoint_up{endpoint="review",group="second",instance="host-1",model="model-b"} 0`,
		`vde_failovers_total{from="primary",role="extractor",to="fallback"} 1`,
		`vde_quota_remaining{app="billing"} 41`,
		`vde_jobs_in_progress{app="billing"} 3`,
		`vde_http_requests_total{route="POST /v1/jobs",status="202"} 1`,
		`vde_http_requests_total{route="none",status="404"} 1`,
		`vde_swept_jobs_total 5`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("scrape lacks %q", want)
		}
	}
}

func TestRuntimeMetricsOptIn(t *testing.T) {
	t.Parallel()
	if out := scrape(t, metrics.New().Handler()); strings.Contains(out, "go_goroutines") || strings.Contains(out, "process_") {
		t.Error("runtime metrics are on by default")
	}
	if out := scrape(t, metrics.New(metrics.WithGoRuntime()).Handler()); !strings.Contains(out, "go_goroutines") {
		t.Error("WithGoRuntime did not expose go_goroutines")
	}
}

func TestRegistriesAreIndependent(t *testing.T) {
	t.Parallel()
	a, b := metrics.New(), metrics.New() // a global registry would panic on the second New
	a.AddSwept(1)
	if out := scrape(t, b.Handler()); !strings.Contains(out, "vde_swept_jobs_total 0\n") {
		t.Error("registries share state")
	}
}

func TestRequireToken(t *testing.T) {
	t.Parallel()
	h := metrics.RequireToken(metrics.New().Handler(), "s3cret")
	tests := []struct {
		name, auth string
		want       int
	}{
		{"none", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"wrong scheme", "Basic s3cret", http.StatusUnauthorized},
		{"right", "Bearer s3cret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer ")
	metrics.RequireToken(metrics.New().Handler(), "").ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an empty configured token let a request through: %d", rec.Code)
	}
}

func TestLoopbackOnly(t *testing.T) {
	t.Parallel()
	h := metrics.LoopbackOnly(metrics.New().Handler())
	tests := []struct {
		remote string
		want   int
	}{
		{"127.0.0.1:5000", http.StatusOK},
		{"[::1]:5000", http.StatusOK},
		{"10.0.0.5:5000", http.StatusNotFound},
		{"203.0.113.9:5000", http.StatusNotFound},
		{"garbage", http.StatusNotFound},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = tt.remote
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("remote %q: status = %d, want %d", tt.remote, rec.Code, tt.want)
		}
	}
}

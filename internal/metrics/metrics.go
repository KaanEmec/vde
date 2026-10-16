package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kaanemec/vde/internal/job"
)

// unknown replaces an empty label value (for example the route of an unmatched request).
const unknown = "none"

// Metrics holds the registry and the series. Its methods are safe for concurrent use.
// *Metrics implements worker.Observer.
type Metrics struct {
	reg *prometheus.Registry

	jobs         *prometheus.CounterVec
	attempts     prometheus.Histogram
	jobDuration  *prometheus.HistogramVec
	providerCall *prometheus.CounterVec
	providerLat  *prometheus.HistogramVec
	failovers    *prometheus.CounterVec
	quota        *prometheus.GaugeVec
	inProgress   *prometheus.GaugeVec
	httpReqs     *prometheus.CounterVec
	swept        prometheus.Counter
}

// Option configures New.
type Option func(*Metrics)

// WithGoRuntime also exposes the Go runtime and process collectors, which are off by
// default.
func WithGoRuntime() Option {
	return func(m *Metrics) {
		m.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}
}

// New returns metrics on a registry of their own, so nothing else in the process
// registers on the exposed endpoint.
func New(opts ...Option) *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}
	m.jobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vde_jobs_total", Help: "Jobs that reached a terminal status, by application and status.",
	}, []string{"app", "status"})
	m.attempts = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "vde_job_attempts", Help: "Extractor attempts per terminal job.",
		Buckets: []float64{1, 2, 3, 4, 5, 6, 8, 10},
	})
	m.jobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "vde_job_duration_seconds", Help: "Seconds from job creation to terminal status, queue time included.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
	}, []string{"status"})
	m.providerCall = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vde_provider_calls_total", Help: "Provider calls by role, endpoint, model and outcome.",
	}, []string{"role", "endpoint", "model", "outcome"})
	m.providerLat = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "vde_provider_latency_seconds", Help: "Seconds per provider call, by role and endpoint.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 40, 80, 160},
	}, []string{"role", "endpoint"})
	m.failovers = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vde_failovers_total", Help: "Moves from one endpoint of a role's group to the next.",
	}, []string{"role", "from", "to"})
	m.quota = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vde_quota_remaining", Help: "Jobs the application may still submit in the current quota window.",
	}, []string{"app"})
	m.inProgress = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vde_jobs_in_progress", Help: "Jobs queued or running, by application.",
	}, []string{"app"})
	m.httpReqs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vde_http_requests_total", Help: "HTTP requests by route pattern and status code.",
	}, []string{"route", "status"})
	m.swept = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vde_swept_jobs_total", Help: "Jobs deleted by the expiry sweeper.",
	})
	m.reg.MustRegister(m.jobs, m.attempts, m.jobDuration, m.providerCall, m.providerLat,
		m.failovers, m.quota, m.inProgress, m.httpReqs, m.swept)
	for _, o := range opts {
		o(m)
	}
	return m
}

// Handler serves the Prometheus text format. It does no authentication; see
// RequireToken and LoopbackOnly.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// JobStarted implements worker.Observer. It records nothing: jobs in progress is a gauge
// the owner of the job store sets with SetJobsInProgress, because a job abandoned at
// shutdown or on a lost lease never reaches JobFinished and a counter pair would drift.
func (m *Metrics) JobStarted(string) {}

// JobFinished implements worker.Observer.
func (m *Metrics) JobFinished(app string, status job.Status, attempts int, d time.Duration) {
	m.jobs.WithLabelValues(label(app), label(string(status))).Inc()
	m.attempts.Observe(float64(attempts))
	m.jobDuration.WithLabelValues(label(string(status))).Observe(d.Seconds())
}

// ObserveHTTP counts one request. route is the matched mux pattern ("" when none matched).
func (m *Metrics) ObserveHTTP(route string, status int) {
	m.httpReqs.WithLabelValues(label(route), strconv.Itoa(status)).Inc()
}

// ObserveProviderCall records one provider call. outcome is a fixed word such as ok,
// retryable or permanent; endpoint and model are configuration ids.
func (m *Metrics) ObserveProviderCall(role, endpoint, model, outcome string, d time.Duration) {
	m.providerCall.WithLabelValues(label(role), label(endpoint), label(model), label(outcome)).Inc()
	m.providerLat.WithLabelValues(label(role), label(endpoint)).Observe(d.Seconds())
}

// ObserveFailover counts a move from endpoint from to endpoint to in a role's group.
func (m *Metrics) ObserveFailover(role, from, to string) {
	m.failovers.WithLabelValues(label(role), label(from), label(to)).Inc()
}

// SetQuotaRemaining sets the remaining quota of an application.
func (m *Metrics) SetQuotaRemaining(app string, n int) {
	m.quota.WithLabelValues(label(app)).Set(float64(n))
}

// SetJobsInProgress sets the queued and running jobs of an application.
func (m *Metrics) SetJobsInProgress(app string, n int) {
	m.inProgress.WithLabelValues(label(app)).Set(float64(n))
}

// AddSwept counts n jobs deleted by the sweeper.
func (m *Metrics) AddSwept(n int) {
	if n > 0 {
		m.swept.Add(float64(n))
	}
}

func label(v string) string {
	if v == "" {
		return unknown
	}
	return v
}

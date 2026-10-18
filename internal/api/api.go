package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	spec "github.com/kaanemec/vde/api"
	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/worker"
)

// HealthChecker reports whether a role's group has a pair not marked down;
// *router.Group implements it.
type HealthChecker interface {
	Healthy() bool
}

// Process roles of vde serve --role (v0.3 epic 07): which parts of the service one process
// runs. Several processes share the queue through the database.
const (
	RoleAll    = "all"    // the API, the worker pool and the sweeper (the default)
	RoleAPI    = "api"    // the API only; worker processes run the jobs
	RoleWorker = "worker" // the worker pool and the sweeper; HTTP serves /healthz and /metrics only
)

// Deps is what the handlers need. Later epics add their fields here: the blob store
// (epic 03), the quota and rate limiter (epic 05) and the worker's submit side (epics 03
// and 04).
type Deps struct {
	Config *config.Config // validated; the handlers never change it
	// Store holds applications, tokens, jobs and the audit trail. Every /v1 request
	// reads its token from it; nil refuses every /v1 request with 500.
	Store job.Store
	// ServiceVersion is sent as X-Service-Version on every /v1/jobs response
	// (contract §11). AppServiceVersion, when set, gives it per application and judge
	// policy for authenticated requests and new jobs (extract.ApplicationServiceVersionFor
	// over the application's stored row, so admin API changes count at once); "" falls
	// back to ServiceVersion. Authenticated requests carry the version of the
	// application's own policy; a submission's response, that of the job's policy.
	ServiceVersion    string
	AppServiceVersion func(job.Application, config.JudgePolicy) string
	Logger            *slog.Logger     // nil means slog.Default()
	Now               func() time.Time // nil means time.Now; tests set it
	// Extractor and Judge are the role groups checked by /healthz. They may be the
	// same group.
	Extractor, Judge HealthChecker
	// Blobs stores document and page bytes (epic 04 submit and delete); nil disables
	// job submission with 503.
	Blobs blob.Store
	// Wake is called after a job is created so the worker pool leases it at once; nil is
	// allowed (the pool then polls).
	Wake func()
	// Metrics receives HTTP, quota and in-progress observations; nil means none.
	Metrics *metrics.Metrics
	// Audit records job events; nil records nothing.
	Audit *audit.Recorder
	// Completions wakes POST /v1/extract when the pool finished its job; nil leaves it
	// to poll the store (sync.go).
	Completions *worker.Notifier
	// PDF prepares PDFs that no group reads natively (ADR 0010): at submit it opens them
	// to count pages and measure the text layer. nil keeps the v0.3 selection (such PDFs
	// need caller pages). It must be the workers' engine configuration.
	PDF document.PDFEngine
	// Role is the process role, RoleAll when "". With RoleAPI, /healthz reports the
	// queue age instead of the role groups (no provider is called here); with RoleWorker
	// only /healthz and /metrics are served.
	Role string
}

type server struct {
	deps   Deps
	log    *slog.Logger
	mux    *http.ServeMux
	limits *rateLimiter // in memory, one per handler (ADR 0003)
	admin  bool         // the admin API's handler (admin.go)
	fetch  *fetcher     // document urls of JSON submissions; nil unless documents.fetch.enabled
	queued queuedCount  // limits.max_queued_jobs, cached count (submit.go)
	// submits holds one token per submission being read and prepared, at most
	// limits.max_submits_in_flight (submit.go).
	submits chan struct{}
}

// route is one registered pattern, in the Go 1.22 ServeMux syntax.
type route struct {
	pattern string
	handler http.Handler
}

// New returns the service's HTTP handler: the /v1 routes, /healthz and /metrics behind
// the middleware chain of wrap.
func New(deps Deps) http.Handler {
	s := newServer(deps)
	return s.wrap(http.HandlerFunc(s.dispatch))
}

// NewAdmin returns the admin API's handler (admin.go): the /admin/v1 routes behind the
// same middleware chain, for its own listener.
func NewAdmin(deps Deps) http.Handler {
	s := newServerWith(deps, true)
	return s.wrap(http.HandlerFunc(s.dispatch))
}

func newServer(deps Deps) *server { return newServerWith(deps, false) }

func newServerWith(deps Deps, admin bool) *server {
	s := &server{deps: deps, log: deps.Logger, mux: http.NewServeMux(), limits: newRateLimiter(), admin: admin}
	if s.log == nil {
		s.log = slog.Default()
	}
	if c := deps.Config; c != nil && c.Documents.Fetch.Enabled {
		s.fetch = newFetcher(c.Documents.Fetch, nil, nil)
	}
	inFlight := defaultSubmitsInFlight
	if c := deps.Config; c != nil && c.Limits.MaxSubmitsInFlight > 0 {
		inFlight = c.Limits.MaxSubmitsInFlight
	}
	s.submits = make(chan struct{}, inFlight)
	routes := s.routes
	if admin {
		routes = s.adminRoutes
	}
	for _, rt := range routes() {
		s.mux.Handle(rt.pattern, rt.handler)
	}
	return s
}

// routes is the complete route table; the OpenAPI test checks it against
// api/openapi.yaml. Each /v1 route passes through authenticate, with the scope it needs,
// then limit: the per-route steps of the chain. jobs:read may poll and read the quota.
func (s *server) routes() []route {
	v1 := func(need string, h http.HandlerFunc) http.Handler { return s.authenticate(need, s.limit(h)) }
	if s.deps.Role == RoleWorker {
		return []route{
			{"GET /healthz", s.healthHandler()},
			{"GET /metrics", s.metricsRoute()},
		}
	}
	return []route{
		{"POST /v1/jobs", v1(auth.ScopeJobs, s.submitJob)},        // submit.go: submission
		{"POST /v1/extract", v1(auth.ScopeJobs, s.extractSync)},   // sync.go: submit and wait
		{"GET /v1/jobs/{id}", v1(auth.ScopeJobsRead, s.getJob)},   // jobs_get.go: poll, first-read deletion
		{"DELETE /v1/jobs/{id}", v1(auth.ScopeJobs, s.deleteJob)}, // jobs_get.go: early delete
		{"GET /v1/quota", v1(auth.ScopeJobsRead, s.getQuota)},     // quota_get.go
		{"GET /v1/openapi.yaml", v1("", s.openAPI)},               // any valid token
		{"GET /healthz", s.healthHandler()},                       // health.go, unauthenticated (contract §12)
		{"GET /metrics", s.metricsRoute()},                        // metrics.go, own protection
	}
}

// dispatch routes r through the mux, answering unmatched requests with JSON 404 and 405
// bodies instead of the mux's plain-text ones. Every /v1/jobs and /v1/extract response
// carries X-Service-Version.
func (s *server) dispatch(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; p == "/v1/jobs" || p == "/v1/extract" || strings.HasPrefix(p, "/v1/jobs/") {
		w.Header().Set(headerServiceVersion, s.deps.ServiceVersion)
	}
	h, pattern := s.mux.Handler(r)
	if pattern != "" {
		s.mux.ServeHTTP(w, r) // sets r.Pattern and the path values
		return
	}
	// The mux's own handler for an unmatched request answers 404, or 405 with Allow.
	probe := &probeWriter{header: http.Header{}}
	h.ServeHTTP(probe, r)
	if probe.status == http.StatusMethodNotAllowed {
		w.Header().Set("Allow", probe.header.Get("Allow"))
		writeError(w, r, newError(codeMethodNotAllowed, msgMethodNotAllowed))
		return
	}
	writeError(w, r, newError(codeNotFound, msgRouteNotFound))
}

// probeWriter records the status and headers of the mux's not-found and
// method-not-allowed handlers and discards their body.
type probeWriter struct {
	header http.Header
	status int
}

func (p *probeWriter) Header() http.Header         { return p.header }
func (p *probeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p *probeWriter) WriteHeader(status int)      { p.status = status }

// openAPI serves the service's own API document (api/openapi.yaml).
func (s *server) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(spec.OpenAPI) // the client went away
}

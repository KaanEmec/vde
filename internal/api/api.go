package api

import (
	"log/slog"
	"net/http"
	"strings"

	spec "github.com/kaanemec/vde/api"
	"github.com/kaanemec/vde/internal/config"
)

// HealthChecker reports whether a role's group has a pair not marked down;
// *router.Group implements it.
type HealthChecker interface {
	Healthy() bool
}

// Deps is what the handlers need. Later epics add their fields here: the job store and
// blob store (epic 03), the token store (epic 02), the quota and rate limiter (epic 05)
// and the worker's submit side (epics 03 and 04).
type Deps struct {
	Config *config.Config // validated; the handlers never change it
	// ServiceVersion is sent as X-Service-Version on every /v1/jobs response
	// (contract §11). Epic 02 may make it per application (see extract.ServiceVersion).
	ServiceVersion string
	Logger         *slog.Logger // nil means slog.Default()
	// Extractor and Judge are the role groups checked by /healthz. They may be the
	// same group.
	Extractor, Judge HealthChecker
}

type server struct {
	deps Deps
	log  *slog.Logger
	mux  *http.ServeMux
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

func newServer(deps Deps) *server {
	s := &server{deps: deps, log: deps.Logger, mux: http.NewServeMux()}
	if s.log == nil {
		s.log = slog.Default()
	}
	for _, rt := range s.routes() {
		s.mux.Handle(rt.pattern, rt.handler)
	}
	return s
}

// routes is the complete route table; the OpenAPI test checks it against
// api/openapi.yaml. Each /v1 route passes through authenticate then limit, the per-route
// steps of the chain.
func (s *server) routes() []route {
	v1 := func(h http.HandlerFunc) http.Handler { return s.authenticate(s.limit(h)) }
	return []route{
		{"POST /v1/jobs", v1(s.notImplemented)},              // epic 04: submission
		{"GET /v1/jobs/{id}", v1(s.notImplemented)},          // epics 03, 06: poll, retention
		{"DELETE /v1/jobs/{id}", v1(s.notImplemented)},       // epic 06: early delete
		{"GET /v1/quota", v1(s.notImplemented)},              // epic 05: quota body
		{"GET /v1/openapi.yaml", v1(s.openAPI)},              // authenticated once epic 02 lands
		{"GET /healthz", http.HandlerFunc(s.healthz)},        // unauthenticated (contract §12)
		{"GET /metrics", http.HandlerFunc(s.notImplemented)}, // epic 07: Prometheus, own protection
	}
}

// dispatch routes r through the mux, answering unmatched requests with JSON 404 and 405
// bodies instead of the mux's plain-text ones. Every /v1/jobs response carries
// X-Service-Version.
func (s *server) dispatch(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; p == "/v1/jobs" || strings.HasPrefix(p, "/v1/jobs/") {
		w.Header().Set("X-Service-Version", s.deps.ServiceVersion)
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

// notImplemented stands in for the handlers of later epics (see routes).
func (s *server) notImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, newError(codeUnavailable, msgNotImplemented))
}

// openAPI serves the service's own API document (api/openapi.yaml).
func (s *server) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(spec.OpenAPI) // the client went away
}

// Reasons /healthz reports when the service cannot accept jobs. Epic 07 adds the
// database and blob store checks.
const (
	reasonExtractorDown = "role_extractor_down"
	reasonJudgeDown     = "role_judge_down"
)

type healthBody struct {
	Status  string       `json:"status"`
	Reasons []string     `json:"reasons,omitempty"`
	Error   *errorDetail `json:"error,omitempty"`
}

// healthz answers 200 {"status":"ok"} when every role group has a pair not marked down,
// else 503 with the reasons and the contract §9 error object.
func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	var reasons []string
	if !healthy(s.deps.Extractor) {
		reasons = append(reasons, reasonExtractorDown)
	}
	if !healthy(s.deps.Judge) {
		reasons = append(reasons, reasonJudgeDown)
	}
	if len(reasons) == 0 {
		writeJSON(w, http.StatusOK, healthBody{Status: "ok"})
		return
	}
	e := newError(codeUnavailable, msgServiceUnhealthy)
	writeJSON(w, e.status, healthBody{
		Status:  "unavailable",
		Reasons: reasons,
		Error:   &errorDetail{Code: e.code, Message: e.message, RequestID: w.Header().Get(headerRequestID)},
	})
}

func healthy(c HealthChecker) bool { return c != nil && c.Healthy() }

package api

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/metrics"
)

// metricsConfig is testConfig with server.insecure_http, so remote peers reach the route.
func metricsConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	return testConfig(t, "server: {insecure_http: true}\n"+yaml)
}

func TestMetricsRoute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		nilReg  bool
		remote  string
		header  [2]string
		want    int
		wantVDE bool // body is the Prometheus text
	}{
		{name: "loopback peer", want: http.StatusOK, wantVDE: true},
		{name: "remote peer refused", remote: "203.0.113.7:4000", want: http.StatusNotFound},
		{name: "proxied loopback refused", header: [2]string{"X-Forwarded-For", "203.0.113.7"}, want: http.StatusNotFound},
		{name: "token required", yaml: "metrics: {token_env: VDE_METRICS_TOKEN}", remote: "203.0.113.7:4000", want: http.StatusUnauthorized},
		{name: "token accepted", yaml: "metrics: {token_env: VDE_METRICS_TOKEN}", remote: "203.0.113.7:4000",
			header: [2]string{"Authorization", "Bearer scrape-secret"}, want: http.StatusOK, wantVDE: true},
		{name: "token unset refuses all", yaml: "metrics: {token_env: VDE_UNSET}",
			header: [2]string{"Authorization", "Bearer "}, want: http.StatusUnauthorized},
		{name: "second listener: not on the main one", yaml: "metrics: {listen: \"127.0.0.1:9090\"}", want: http.StatusNotFound},
		{name: "no registry", nilReg: true, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := metrics.New(false)
			if tt.nilReg {
				m = nil
			}
			h := New(testDeps(t, Deps{Config: metricsConfig(t, tt.yaml), Store: newTestStore(t), Logger: slog.New(slog.DiscardHandler),
				Extractor: fakeHealth(true), Judge: fakeHealth(true), Metrics: m}))
			serve(h, http.MethodGet, "/healthz", nil) // one observed request
			w := serve(h, http.MethodGet, "/metrics", func(r *http.Request) {
				if tt.remote != "" {
					r.RemoteAddr = tt.remote
				}
				if tt.header[0] != "" {
					r.Header.Set(tt.header[0], tt.header[1])
				}
			})
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tt.want, w.Body)
			}
			if !tt.wantVDE {
				if strings.Contains(w.Body.String(), "vde_") {
					t.Errorf("refused request got metrics: %s", w.Body)
				}
				return
			}
			if want := `vde_http_requests_total{route="GET /healthz",status="200"} 1`; !strings.Contains(w.Body.String(), want) {
				t.Errorf("metrics lack %s:\n%s", want, w.Body)
			}
		})
	}
}

// TestMetricsHTTPLabels: the route label is the mux pattern, never the request path,
// so job ids and unmatched paths cannot reach a label.
func TestMetricsHTTPLabels(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	deps := f.s.deps
	deps.Metrics = metrics.New(false)
	h := handlerOf(newServer(deps))
	serve(h, http.MethodGet, "/v1/jobs/j_secretid", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) })
	serve(h, http.MethodGet, "/no/such/path-secret", nil)
	w := serve(h, http.MethodGet, "/metrics", nil)
	body := w.Body.String()
	for _, want := range []string{
		`vde_http_requests_total{route="GET /v1/jobs/{id}",status="404"} 1`,
		`vde_http_requests_total{route="none",status="404"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	if strings.Contains(body, "secret") {
		t.Errorf("metrics carry request data:\n%s", body)
	}
}

// TestNewMetrics: the metrics.listen handler serves GET /metrics alone, behind the token
// when metrics.token_env is set, and counts its own requests.
func TestNewMetrics(t *testing.T) {
	t.Parallel()
	m := metrics.New(false)
	h := NewMetrics(m, metricsConfig(t, "metrics: {listen: \"127.0.0.1:9090\", token_env: VDE_METRICS_TOKEN}").Metrics)
	if w := serve(h, http.MethodGet, "/metrics", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("without the token: %d, want 401", w.Code)
	}
	if w := serve(h, http.MethodGet, "/healthz", nil); w.Code != http.StatusNotFound {
		t.Errorf("GET /healthz: %d, want 404", w.Code)
	}
	w := serve(h, http.MethodGet, "/metrics", func(r *http.Request) { r.Header.Set("Authorization", "Bearer scrape-secret") })
	if w.Code != http.StatusOK {
		t.Fatalf("with the token: %d, want 200", w.Code)
	}
	for _, want := range []string{
		`vde_http_requests_total{route="GET /metrics",status="401"} 1`,
		`vde_http_requests_total{route="none",status="404"} 1`,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}

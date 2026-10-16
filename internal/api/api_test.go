package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	spec "github.com/kaanemec/vde/api"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/config"
)

const testServiceVersion = "vde-0.0.0-dev+0123abcd"

type fakeHealth bool

func (f fakeHealth) Healthy() bool { return bool(f) }

// newTestServer returns a server with healthy roles and a store holding the enabled
// application testApp, whose log goes to the returned buffer. testToken issues tokens.
func newTestServer(t *testing.T, trustedProxy bool) (*server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	cfg := &config.Config{Server: config.Server{TrustedProxy: trustedProxy}}
	return newServer(Deps{
		Config:         cfg,
		Store:          newTestStore(t, testApp),
		ServiceVersion: testServiceVersion,
		Logger:         slog.New(slog.NewJSONHandler(&logs, nil)),
		Extractor:      fakeHealth(true),
		Judge:          fakeHealth(true),
	}), &logs
}

func handlerOf(s *server) http.Handler { return s.wrap(http.HandlerFunc(s.dispatch)) }

// serve runs one request from a loopback peer unless remote is set.
func serve(h http.Handler, method, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "127.0.0.1:50000"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// decodeError checks the contract §9 body shape and returns its error object.
func decodeError(t *testing.T, w *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	e, ok := body["error"]
	if !ok || len(body) != 1 {
		t.Fatalf("error body %q: want exactly the key error", w.Body.String())
	}
	for k := range e {
		if k != "code" && k != "message" && k != "request_id" {
			t.Errorf("error body has unexpected key %q", k)
		}
	}
	if e["message"] == "" {
		t.Error("error message is empty")
	}
	if e["request_id"] != w.Header().Get(headerRequestID) {
		t.Errorf("request_id = %q, want the X-Request-Id header %q", e["request_id"], w.Header().Get(headerRequestID))
	}
	return errorDetail{Code: e["code"], Message: e["message"], RequestID: e["request_id"]}
}

func TestRequestID(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", maxRequestIDChars)
	tests := []struct {
		name   string
		header string
		echo   bool
	}{
		{name: "echoed", header: "trace-42", echo: true},
		{name: "128 chars echoed", header: long, echo: true},
		{name: "129 chars replaced", header: long + "a"},
		{name: "absent generated", header: ""},
		{name: "space replaced", header: "a b"},
		{name: "non-ASCII replaced", header: "café"},
	}
	s, _ := newTestServer(t, false)
	h := handlerOf(s)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := serve(h, http.MethodGet, "/nope", func(r *http.Request) {
				if tt.header != "" {
					r.Header.Set(headerRequestID, tt.header)
				}
			})
			got := w.Header().Get(headerRequestID)
			if tt.echo && got != tt.header {
				t.Errorf("X-Request-Id = %q, want %q echoed", got, tt.header)
			}
			if !tt.echo && (!strings.HasPrefix(got, "r_") || len(got) != 2+26) {
				t.Errorf("X-Request-Id = %q, want a generated r_<ULID>", got)
			}
			decodeError(t, w) // request_id in the body equals the header
			if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("security headers missing: %v", w.Header())
			}
		})
	}
	t.Run("fresh per request", func(t *testing.T) {
		t.Parallel()
		a := serve(h, http.MethodGet, "/healthz", nil).Header().Get(headerRequestID)
		b := serve(h, http.MethodGet, "/healthz", nil).Header().Get(headerRequestID)
		if a == b {
			t.Errorf("two requests got the same generated id %q", a)
		}
	})
}

func TestRequireHTTPS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		trustedProxy bool
		remote       string
		tls          bool
		proto        string
		wantRefused  bool
	}{
		{name: "plain from remote host refused", remote: "203.0.113.7:4000", wantRefused: true},
		{name: "plain from IPv4 loopback", remote: "127.0.0.1:4000"},
		{name: "plain from IPv6 loopback", remote: "[::1]:4000"},
		{name: "TLS from remote host", remote: "203.0.113.7:4000", tls: true},
		{name: "forwarded proto ignored without trusted proxy", remote: "203.0.113.7:4000", proto: "https", wantRefused: true},
		{name: "proxy forwards https", trustedProxy: true, remote: "10.0.0.2:4000", proto: "https"},
		{name: "proxy forwards HTTPS chain", trustedProxy: true, remote: "10.0.0.2:4000", proto: "HTTPS, http"},
		{name: "proxy forwards http refused", trustedProxy: true, remote: "10.0.0.2:4000", proto: "http", wantRefused: true},
		{name: "proxy without header refused", trustedProxy: true, remote: "10.0.0.2:4000", wantRefused: true},
		{name: "proxy mode has no loopback exemption", trustedProxy: true, remote: "127.0.0.1:4000", wantRefused: true},
		{name: "proxy mode with TLS", trustedProxy: true, remote: "10.0.0.2:4000", tls: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _ := newTestServer(t, tt.trustedProxy)
			w := serve(handlerOf(s), http.MethodGet, "/healthz", func(r *http.Request) {
				r.RemoteAddr = tt.remote
				if tt.tls {
					r.TLS = &tls.ConnectionState{}
				}
				if tt.proto != "" {
					r.Header.Set("X-Forwarded-Proto", tt.proto)
				}
			})
			if !tt.wantRefused {
				if w.Code != http.StatusOK {
					t.Errorf("status = %d, want 200; body %s", w.Code, w.Body)
				}
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if e := decodeError(t, w); e.Code != codeBadRequest || e.Message != msgHTTPSRequired {
				t.Errorf("error = %+v, want bad_request %q", e, msgHTTPSRequired)
			}
		})
	}
}

func TestPanicRecovery(t *testing.T) {
	t.Parallel()
	const secret = "document text that must not leak"
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "panic with content", handler: func(http.ResponseWriter, *http.Request) { panic(secret) }},
		{name: "runtime error", handler: func(_ http.ResponseWriter, r *http.Request) {
			_ = r.URL.Path[len(r.URL.Path)+len(secret)]
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, logs := newTestServer(t, false)
			w := serve(s.wrap(tt.handler), http.MethodGet, "/v1/jobs/x", nil)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
			if e := decodeError(t, w); e.Code != codeInternal {
				t.Errorf("code = %q, want internal", e.Code)
			}
			body := w.Body.String()
			for _, leak := range []string{secret, "goroutine", ".go:", "panic"} {
				if strings.Contains(body, leak) {
					t.Errorf("body %q contains %q", body, leak)
				}
			}
			if strings.Contains(logs.String(), secret) {
				t.Errorf("log contains the panic's content: %s", logs)
			}
			if !strings.Contains(logs.String(), `"msg":"handler panic"`) {
				t.Errorf("panic not logged: %s", logs)
			}
		})
	}
	t.Run("after the header was written", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestServer(t, false)
		w := serve(s.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			panic("late")
		})), http.MethodGet, "/", nil)
		if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
			t.Errorf("got %d %q, want the written 202 and no error body appended", w.Code, w.Body)
		}
	})
}

func TestUnmatchedRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method, target string
		wantStatus     int
		wantCode       string
		wantAllow      []string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, codeNotFound, nil},
		{http.MethodGet, "/v1/jobs/j_1/extra", http.StatusNotFound, codeNotFound, nil},
		{http.MethodGet, "/v1", http.StatusNotFound, codeNotFound, nil},
		{http.MethodPut, "/v1/jobs", http.StatusMethodNotAllowed, codeMethodNotAllowed, []string{"POST"}},
		{http.MethodGet, "/v1/jobs", http.StatusMethodNotAllowed, codeMethodNotAllowed, []string{"POST"}},
		{http.MethodPost, "/v1/jobs/j_1", http.StatusMethodNotAllowed, codeMethodNotAllowed, []string{"GET", "DELETE"}},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, codeMethodNotAllowed, []string{"GET"}},
	}
	s, _ := newTestServer(t, false)
	h := handlerOf(s)
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()
			w := serve(h, tt.method, tt.target, nil)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tt.wantStatus, w.Body)
			}
			if e := decodeError(t, w); e.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", e.Code, tt.wantCode)
			}
			allow := w.Header().Get("Allow")
			for _, m := range tt.wantAllow {
				if !strings.Contains(allow, m) {
					t.Errorf("Allow = %q, want it to contain %s", allow, m)
				}
			}
		})
	}
}

func TestPlaceholderRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method, target string
		serviceVersion bool
	}{
		{http.MethodPost, "/v1/jobs", true},
	}
	s, _ := newTestServer(t, false)
	h := handlerOf(s)
	token := testToken(t, s, auth.ScopeJobs)
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()
			w := serve(h, tt.method, tt.target, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", w.Code)
			}
			if e := decodeError(t, w); e.Code != codeUnavailable {
				t.Errorf("code = %q, want unavailable", e.Code)
			}
			got := w.Header().Get("X-Service-Version")
			if tt.serviceVersion && got != testServiceVersion {
				t.Errorf("X-Service-Version = %q, want %q", got, testServiceVersion)
			}
			if !tt.serviceVersion && got != "" {
				t.Errorf("X-Service-Version = %q outside /v1/jobs", got)
			}
		})
	}
	t.Run("405 under /v1/jobs carries the service version", func(t *testing.T) {
		t.Parallel()
		if got := serve(h, http.MethodPatch, "/v1/jobs/j_1", nil).Header().Get("X-Service-Version"); got != testServiceVersion {
			t.Errorf("X-Service-Version = %q, want %q", got, testServiceVersion)
		}
	})
}

func TestAccessLog(t *testing.T) {
	t.Parallel()
	s, logs := newTestServer(t, false)
	token := testToken(t, s, auth.ScopeJobs)
	w := serve(handlerOf(s), http.MethodGet, "/v1/jobs/j_secretid?token=hunter2", func(r *http.Request) {
		r.Header.Set(headerRequestID, "trace-7")
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown job", w.Code)
	}
	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("want one JSON log line, got %q: %v", logs, err)
	}
	want := map[string]any{
		"msg": "request", "method": "GET", "route": "GET /v1/jobs/{id}",
		"status": float64(404), "request_id": "trace-7", "app_id": testApp,
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("log %s = %v, want %v", k, line[k], v)
		}
	}
	for k := range line {
		if _, ok := want[k]; !ok && k != "time" && k != "level" && k != "duration_ms" {
			t.Errorf("log has key %q outside the fixed set", k)
		}
	}
	for _, leak := range []string{"hunter2", "token", "secretid", token[len(auth.Prefix):]} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("log contains %q: %s", leak, logs)
		}
	}
}

func TestOpenAPIRoute(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, false)
	if w := serve(handlerOf(s), http.MethodGet, "/v1/openapi.yaml", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without a token: status = %d, want 401", w.Code)
	}
	token := testToken(t, s, auth.ScopeJobsRead)
	w := serve(handlerOf(s), http.MethodGet, "/v1/openapi.yaml", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/yaml" {
		t.Fatalf("got %d %q, want 200 application/yaml", w.Code, w.Header().Get("Content-Type"))
	}
	if !bytes.Equal(w.Body.Bytes(), spec.OpenAPI) {
		t.Error("body is not api/openapi.yaml")
	}
}

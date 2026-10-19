package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
)

const (
	testApp  = "travelinbox-test"
	otherApp = "other-app"
)

var testNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// auditStore records the audit events it is given, so tests can check them.
type auditStore struct {
	job.Store
	mu     sync.Mutex
	events []job.AuditEvent
}

func (s *auditStore) AppendAudit(ctx context.Context, e job.AuditEvent) (int64, error) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
	return s.Store.AppendAudit(ctx, e)
}

func (s *auditStore) last() job.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return job.AuditEvent{}
	}
	return s.events[len(s.events)-1]
}

func newTestStore(t *testing.T, apps ...string) *auditStore {
	t.Helper()
	db, err := job.Open(filepath.Join(t.TempDir(), "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range apps {
		app := job.Application{ID: id, MaxAttempts: 3, QuotaLimit: 10, QuotaWindow: "P1D",
			RequestsPerMinute: 300, SubmitsPerMinute: 30, MaxInProgress: 20, RetentionDefault: "24h"}
		if err := db.UpsertApplication(context.Background(), app, testNow); err != nil {
			t.Fatal(err)
		}
	}
	return &auditStore{Store: db}
}

// testToken issues a token for testApp valid for a day around the real clock and the
// test clock.
func testToken(t *testing.T, s *server, scopes ...string) string {
	t.Helper()
	return issueToken(t, s.deps.Store, testApp, scopes, time.Now().Add(24*time.Hour))
}

func issueToken(t *testing.T, store job.Store, app string, scopes []string, expires time.Time) string {
	t.Helper()
	token, _, err := auth.Create(context.Background(), store, auth.Request{
		ApplicationID: app, Scopes: scopes, ExpiresAt: expires,
	}, 10*365*24*time.Hour, time.Now().Add(-365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// countingBody records whether the handler read the request body.
type countingBody struct {
	r    io.Reader
	read atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read.Add(int64(n))
	return n, err
}

func TestAuthenticateRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t, testApp, otherApp)
	var logs bytes.Buffer
	var calls atomic.Int64 // the handler behind auth stands in for every provider call
	s := newServer(Deps{
		Config: &config.Config{}, Store: store, ServiceVersion: testServiceVersion,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Now: func() time.Time { return testNow },
	})
	h := s.wrap(s.authenticate(auth.ScopeJobs, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})))

	good := issueToken(t, store, testApp, []string{auth.ScopeJobs}, testNow.Add(time.Hour))
	expired := issueToken(t, store, testApp, []string{auth.ScopeJobs}, testNow)
	revoked := issueToken(t, store, testApp, []string{auth.ScopeJobs}, testNow.Add(time.Hour))
	revokedID, _, _ := auth.Parse(revoked)
	if err := store.RevokeToken(ctx, revokedID, testNow); err != nil {
		t.Fatal(err)
	}
	disabled := issueToken(t, store, otherApp, []string{auth.ScopeJobs}, testNow.Add(time.Hour))
	if _, err := store.DisableApplicationsNotIn(ctx, []string{testApp}, testNow); err != nil {
		t.Fatal(err)
	}
	unknown, _, _, _ := auth.Generate()
	wrongSecret := good[:len(good)-3] + "AAA"
	if wrongSecret == good {
		wrongSecret = good[:len(good)-3] + "QQQ"
	}

	tests := []struct {
		name    string
		header  []string // Authorization values
		reason  auth.Reason
		wantApp string
	}{
		{name: "no header", reason: auth.ReasonMissing},
		{name: "basic scheme", header: []string{"Basic dXNlcjpwYXNz"}, reason: auth.ReasonMalformed},
		{name: "bearer without token", header: []string{"Bearer "}, reason: auth.ReasonMalformed},
		{name: "two headers", header: []string{"Bearer " + good, "Bearer " + good}, reason: auth.ReasonMalformed},
		{name: "malformed token", header: []string{"Bearer vde_short_x"}, reason: auth.ReasonMalformed},
		{name: "unknown id", header: []string{"Bearer " + unknown}, reason: auth.ReasonUnknown},
		{name: "wrong secret", header: []string{"Bearer " + wrongSecret}, reason: auth.ReasonWrongSecret, wantApp: testApp},
		{name: "expired", header: []string{"Bearer " + expired}, reason: auth.ReasonExpired, wantApp: testApp},
		{name: "revoked", header: []string{"Bearer " + revoked}, reason: auth.ReasonRevoked, wantApp: testApp},
		{name: "disabled application", header: []string{"Bearer " + disabled}, reason: auth.ReasonDisabled, wantApp: otherApp},
	}
	var message string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &countingBody{r: strings.NewReader(strings.Repeat("x", 1<<20))}
			w := serve(h, http.MethodPost, "/v1/jobs", func(r *http.Request) {
				r.Body = io.NopCloser(body)
				r.ContentLength = 1 << 20
				for _, v := range tt.header {
					r.Header.Add("Authorization", v)
				}
			})
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", w.Code, w.Body)
			}
			e := decodeError(t, w)
			if e.Code != codeUnauthorized {
				t.Errorf("code = %q, want unauthorized", e.Code)
			}
			if message != "" && e.Message != message {
				t.Errorf("message %q differs from %q: refusals must be indistinguishable", e.Message, message)
			}
			message = e.Message
			if w.Header().Get("WWW-Authenticate") == "" || w.Header().Get("Connection") != "close" {
				t.Errorf("headers = %v, want WWW-Authenticate and Connection: close", w.Header())
			}
			if n := body.read.Load(); n != 0 {
				t.Errorf("auth read %d body bytes before refusing", n)
			}
			ev := store.last()
			var details map[string]string
			_ = json.Unmarshal(ev.Details, &details)
			if ev.Kind != string(audit.AuthFailedKind) || details["reason"] != string(tt.reason) || ev.ApplicationID != tt.wantApp || len(details) != 1 {
				t.Errorf("audit = %+v %s, want auth.failed reason %s application %q", ev, ev.Details, tt.reason, tt.wantApp)
			}
		})
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("handler behind auth ran %d times for refused requests, want 0", n)
	}
	if strings.Contains(logs.String(), good[len(auth.Prefix)+auth.IDLength+1:]) {
		t.Error("log contains a token secret")
	}
	if w := serve(h, http.MethodPost, "/v1/jobs", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+good) }); w.Code != http.StatusAccepted || calls.Load() != 1 {
		t.Errorf("valid token: status %d, calls %d; want 202 and 1", w.Code, calls.Load())
	}
}

// TestAuthFailedAuditCapped: refused requests, which need no token, write at most
// authFailedAuditBurst auth.failed rows at once and authFailedAuditPerSecond after that;
// the rest are counted in vde_audit_auth_failed_dropped_total and still answer 401.
func TestAuthFailedAuditCapped(t *testing.T) {
	t.Parallel()
	store := newTestStore(t, testApp)
	var mu sync.Mutex
	now := testNow
	m := metrics.New()
	s := newServer(Deps{
		Config: &config.Config{}, Store: store, ServiceVersion: testServiceVersion, Metrics: m,
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
	})
	h := handlerOf(s)
	refused := func(n int) {
		t.Helper()
		for range n {
			if w := serve(h, http.MethodGet, "/v1/quota", nil); w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
		}
	}
	rows := func() int {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.events)
	}
	dropped := func() string {
		for line := range strings.Lines(serve(h, http.MethodGet, "/metrics", nil).Body.String()) {
			if strings.HasPrefix(line, "vde_audit_auth_failed_dropped_total ") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}

	refused(authFailedAuditBurst + 10)
	if got := rows(); got != authFailedAuditBurst {
		t.Errorf("audit rows after a burst of %d = %d, want %d", authFailedAuditBurst+10, got, authFailedAuditBurst)
	}
	if got := dropped(); got != "vde_audit_auth_failed_dropped_total 10" {
		t.Errorf("dropped counter = %q, want 10", got)
	}

	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	refused(authFailedAuditPerSecond + 5)
	if got := rows(); got != authFailedAuditBurst+authFailedAuditPerSecond {
		t.Errorf("audit rows one second later = %d, want %d", got, authFailedAuditBurst+authFailedAuditPerSecond)
	}
	if got := dropped(); got != "vde_audit_auth_failed_dropped_total 15" {
		t.Errorf("dropped counter = %q, want 15", got)
	}
}

func TestAuthenticateScopes(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, false)
	h := handlerOf(s)
	readOnly := testToken(t, s, auth.ScopeJobsRead)
	full := testToken(t, s, auth.ScopeJobs)
	tests := []struct {
		token, method, target string
		want                  int
	}{
		{readOnly, http.MethodPost, "/v1/jobs", http.StatusForbidden},
		{readOnly, http.MethodDelete, "/v1/jobs/j_1", http.StatusForbidden},
		{readOnly, http.MethodGet, "/v1/jobs/j_1", http.StatusNotFound}, // past auth: an unknown job
		{readOnly, http.MethodGet, "/v1/quota", http.StatusOK},
		{readOnly, http.MethodGet, "/v1/openapi.yaml", http.StatusOK},
		{full, http.MethodPost, "/v1/jobs", http.StatusServiceUnavailable},
		{full, http.MethodDelete, "/v1/jobs/j_1", http.StatusNotFound},
		{full, http.MethodGet, "/v1/quota", http.StatusOK},
	}
	for _, tt := range tests {
		w := serve(h, tt.method, tt.target, func(r *http.Request) {
			r.Header.Set("Authorization", "bearer "+tt.token) // the scheme is case-insensitive
			r.Body = io.NopCloser(strings.NewReader("{}"))
			r.ContentLength = 2
		})
		if w.Code != tt.want {
			t.Errorf("%s %s with %s: status = %d, want %d", tt.method, tt.target, tt.token[:12], w.Code, tt.want)
			continue
		}
		if tt.want == http.StatusForbidden {
			if e := decodeError(t, w); e.Code != codeForbidden || w.Header().Get("Connection") != "close" {
				t.Errorf("403: code %q, headers %v; want forbidden and Connection: close", e.Code, w.Header())
			}
		}
	}
}

func TestAuthenticateContext(t *testing.T) {
	t.Parallel()
	store := newTestStore(t, testApp)
	now := testNow
	s := newServer(Deps{
		Config: &config.Config{}, Store: store, Logger: slog.New(slog.DiscardHandler),
		ServiceVersion: testServiceVersion,
		AppServiceVersion: func(a job.Application, _ config.JudgePolicy) string {
			if a.ID == testApp {
				return "vde-0.0.0-dev+app00001"
			}
			return ""
		},
		Now: func() time.Time { return now },
	})
	var gotApp job.Application
	var gotScopes []string
	h := s.wrap(http.HandlerFunc(s.dispatch))
	s.mux = http.NewServeMux()
	s.mux.Handle("GET /v1/jobs/{id}", s.authenticate(auth.ScopeJobsRead, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotApp, _ = ApplicationFrom(r.Context())
		p, _ := r.Context().Value(principalKey{}).(auth.Principal)
		gotScopes = p.Token.Scopes
	})))
	token := issueToken(t, store, testApp, []string{auth.ScopeJobsRead, auth.ScopeJobs}, testNow.Add(time.Hour))
	id, _, _ := auth.Parse(token)
	get := func() *bytes.Buffer {
		w := serve(h, http.MethodGet, "/v1/jobs/j_1", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if v := w.Header().Get(headerServiceVersion); v != "vde-0.0.0-dev+app00001" {
			t.Errorf("X-Service-Version = %q, want the application's", v)
		}
		return w.Body
	}
	lastUsed := func() time.Time {
		tok, err := store.GetTokenByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return tok.LastUsedAt
	}

	get()
	if gotApp.ID != testApp || !gotApp.Enabled || len(gotScopes) != 2 {
		t.Errorf("context: application %+v scopes %q", gotApp, gotScopes)
	}
	if !lastUsed().Equal(testNow) {
		t.Errorf("last_used_at = %s, want %s", lastUsed(), testNow)
	}
	now = testNow.Add(30 * time.Second)
	get()
	if !lastUsed().Equal(testNow) {
		t.Errorf("last_used_at moved to %s within a minute", lastUsed())
	}
	now = testNow.Add(61 * time.Second)
	get()
	if !lastUsed().Equal(now) {
		t.Errorf("last_used_at = %s after a minute, want %s", lastUsed(), now)
	}
	if _, ok := ApplicationFrom(context.Background()); ok {
		t.Error("ApplicationFrom an unauthenticated context reported ok")
	}
}

func TestAuthenticateWithoutStore(t *testing.T) {
	t.Parallel()
	h := New(Deps{Logger: slog.New(slog.DiscardHandler)})
	if w := serve(h, http.MethodGet, "/v1/quota", nil); w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when no store is wired", w.Code)
	}
}

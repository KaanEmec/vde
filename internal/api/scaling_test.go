package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

func (brokenStore) OldestQueuedAt(context.Context) (time.Time, error) {
	return time.Time{}, errors.New("database is locked")
}

type queueAgeResponse struct {
	healthResponse
	QueueAgeSeconds *int64 `json:"queue_age_seconds"`
}

// TestHealthzAPIRole: an API-only process reports the age of the oldest queued job and
// does not depend on the role groups, which only worker processes call.
func TestHealthzAPIRole(t *testing.T) {
	t.Parallel()
	store := newTestStore(t, "app")
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	h := New(Deps{Store: store, Logger: slog.New(slog.DiscardHandler), Role: RoleAPI, Now: func() time.Time { return now },
		Extractor: fakeHealth(false), Judge: fakeHealth(false)})
	get := func() (int, queueAgeResponse, string) {
		w := serve(h, http.MethodGet, "/healthz", nil)
		var body queueAgeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %q: %v", w.Body, err)
		}
		return w.Code, body, w.Body.String()
	}
	if code, _, raw := get(); code != http.StatusOK || strings.TrimSpace(raw) != `{"status":"ok","queue_age_seconds":0}` {
		t.Errorf("empty queue: %d %s, want 200 with queue age 0 and no role reasons", code, raw)
	}
	j := &job.Job{ApplicationID: "app", ServiceVersion: "v", Representation: "text", MediaType: "text/plain",
		Retention: job.RetentionDefault, CreatedAt: now.Add(-90 * time.Second)}
	if err := store.CreateJob(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if code, body, raw := get(); code != http.StatusOK || body.QueueAgeSeconds == nil || *body.QueueAgeSeconds != 90 {
		t.Errorf("one job queued 90 s ago: %d %s, want 200 with queue_age_seconds 90", code, raw)
	}
	broken := New(Deps{Store: brokenStore{store}, Logger: slog.New(slog.DiscardHandler), Role: RoleAPI})
	if code, body, _ := getHealth(t, broken); code != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{reasonDB}) {
		t.Errorf("database down: %d %v, want 503 [db]", code, body.Reasons)
	}
}

// TestWorkerRoleRoutes: a worker process serves /healthz and /metrics, not the API.
func TestWorkerRoleRoutes(t *testing.T) {
	t.Parallel()
	h := New(Deps{Store: newTestStore(t), Logger: slog.New(slog.DiscardHandler), Role: RoleWorker,
		Extractor: fakeHealth(true), Judge: fakeHealth(true)})
	if code, _, raw := getHealth(t, h); code != http.StatusOK {
		t.Errorf("/healthz = %d %s, want 200", code, raw)
	}
	for _, target := range []string{"/v1/jobs/j_1", "/v1/quota", "/v1/openapi.yaml"} {
		if w := serve(h, http.MethodGet, target, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s on a worker = %d, want 404", target, w.Code)
		}
	}
}

// TestSharedRateLimit is the acceptance of job 07-02: with rate.shared: true two API
// replicas (two handlers, each with its own in-memory limiter) on one database enforce one
// combined limit; with the default each enforces its own.
func TestSharedRateLimit(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			t.Parallel()
			checkSharedRate(t, newTestStore(t), shared)
		})
	}
}

// TestSharedRateLimitPostgres is TestSharedRateLimit on PostgreSQL (VDE_TEST_POSTGRES).
func TestSharedRateLimitPostgres(t *testing.T) {
	t.Parallel()
	checkSharedRate(t, openTestPostgres(t), true)
}

func checkSharedRate(t *testing.T, store job.Store, shared bool) {
	t.Helper()
	const app, perMinute = "rated", 5
	if err := store.UpsertApplication(t.Context(), job.Application{ID: app, Name: app, MaxAttempts: 3, QuotaLimit: 100,
		QuotaWindow: "P1D", RequestsPerMinute: perMinute, SubmitsPerMinute: perMinute, MaxInProgress: 20,
		RetentionDefault: "24h"}, testNow); err != nil {
		t.Fatal(err)
	}
	token := issueToken(t, store, app, []string{auth.ScopeJobs}, time.Now().Add(365*24*time.Hour))
	cfg := &config.Config{Rate: config.ServiceRate{Shared: shared}}
	now := time.Date(2026, 10, 16, 12, 0, 10, 0, time.UTC)
	replicas := make([]http.Handler, 2)
	for i := range replicas {
		replicas[i] = New(Deps{Config: cfg, Store: store, Logger: slog.New(slog.DiscardHandler), Now: func() time.Time { return now },
			Extractor: fakeHealth(true), Judge: fakeHealth(true)})
	}
	admitted := 0
	var last *http.Response
	for i := range 4 * perMinute {
		w := serve(replicas[i%2], http.MethodGet, "/v1/quota", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
		switch w.Code {
		case http.StatusOK:
			admitted++
		case http.StatusTooManyRequests:
			last = w.Result()
		default:
			t.Fatalf("request %d: status %d %s", i+1, w.Code, w.Body)
		}
	}
	want := perMinute
	if !shared {
		want = 2 * perMinute // each replica its own bucket
	}
	if admitted != want {
		t.Errorf("admitted %d of %d requests across two replicas, want %d", admitted, 4*perMinute, want)
	}
	if shared && (last == nil || last.Header.Get("Retry-After") != "50") {
		t.Errorf("refusal %v, want Retry-After 50 (the next minute)", last)
	}
}

// openTestPostgres opens a store in a fresh schema of the VDE_TEST_POSTGRES database,
// dropped at cleanup, or skips the test.
func openTestPostgres(t *testing.T) *job.DB {
	t.Helper()
	base := os.Getenv("VDE_TEST_POSTGRES")
	if base == "" {
		t.Skip("VDE_TEST_POSTGRES is not set")
	}
	schema := fmt.Sprintf("vde_api_test_%d_%d", os.Getpid(), time.Now().UnixNano()%1e9)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	s, err := job.OpenPostgres(t.Context(), u.String(), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestSubmitReadsEveryRoleGroup: the representation must be readable by every group that
// may serve a role (extract.RoleCapabilities). Both primary groups read PDFs natively; an
// escalation group that reads only page images makes a PDF without pages unsupported.
func TestSubmitReadsEveryRoleGroup(t *testing.T) {
	t.Parallel()
	pdfReaders := func(cfg *config.Config) {
		for _, name := range []string{"ext", "rev"} {
			for id, m := range cfg.Endpoints[name].Models {
				m.Accepts = append(m.Accepts, "application/pdf")
				cfg.Endpoints[name].Models[id] = m
			}
		}
	}
	pdf := withPart(validParts(), mpart{name: partDocument, ctype: "application/pdf", data: []byte(pdfBytes)})

	rg := newSubmitRig(t, submitRigOptions{cfg: pdfReaders})
	if w := rg.submit(t, pdf, nil); w.Code != http.StatusAccepted {
		t.Fatalf("PDF with native readers: %d %s, want 202", w.Code, w.Body)
	}

	rg = newSubmitRig(t, submitRigOptions{cfg: func(cfg *config.Config) {
		pdfReaders(cfg)
		cfg.Endpoints["plain"] = &config.Endpoint{Provider: "fake",
			Models: map[string]config.Model{"plain-model": {Accepts: []string{"text/plain", "image/png"}}}}
		cfg.Groups["plain"] = &config.Group{Endpoints: []config.GroupEntry{{Endpoint: "plain", Model: "plain-model"}}}
		cfg.Roles.Extractor.Escalation = []config.Escalation{{FromAttempt: 2, Group: "plain"}}
	}})
	w := rg.submit(t, pdf, nil)
	if w.Code != http.StatusUnsupportedMediaType || decodeError(t, w).Code != codeUnsupportedDocument {
		t.Errorf("PDF without pages, escalation group without a PDF reader: %d %s, want 415 %s", w.Code, w.Body, codeUnsupportedDocument)
	}
}

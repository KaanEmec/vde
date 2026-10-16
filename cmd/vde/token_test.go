package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// serviceConfig is a configuration on the fake provider with a data directory; %s is
// replaced by the applications list.
const serviceConfig = `
server: {data_dir: "DATA"}
endpoints:
  scripted:
    provider: fake
    models:
      m: {accepts: [application/pdf, image/png, image/jpeg, text/plain, text/html]}
groups:
  main: {endpoints: [{endpoint: scripted, model: m}]}
roles: {extractor: {group: main}, judge: {group: main}}
applications:
APPS`

func writeServiceConfig(t *testing.T, dataDir string, apps ...string) string {
	t.Helper()
	var list strings.Builder
	for _, a := range apps {
		list.WriteString("  - {id: " + a + "}\n")
	}
	text := strings.NewReplacer("DATA", dataDir, "APPS", list.String()).Replace(serviceConfig)
	path := filepath.Join(t.TempDir(), "vde.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// startService runs serve on a loopback port with every endpoint answered by p and
// returns the base URL and a function that stops it.
func startService(t *testing.T, path string, p provider.Provider) (string, func()) {
	t.Helper()
	cfg, _, err := loadConfigEnv(path, false, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	build := func(*config.Endpoint) (provider.Provider, error) { return p, nil }
	go func() { done <- serve(ctx, cfg, ln, slog.New(slog.DiscardHandler), build) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}
	t.Cleanup(stop)
	base := "http://" + ln.Addr().String()
	for deadline := time.Now().Add(5 * time.Second); ; { // serving means the applications are synced
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not answer: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return base, stop
}

// cli runs a vde command in-process and returns its exit code, stdout and stderr.
func cli(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func request(t *testing.T, method, url, token string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("--x--"))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestAuthEndToEnd is contract §14 item 2 at the HTTP boundary: missing, malformed,
// unknown, wrong-secret, revoked, expired and disabled-application tokens get 401, a
// read-only token submitting gets 403, and the provider is never called, no job is
// stored. It also covers the token CLI against the database serve uses and the
// application sync on restart.
func TestAuthEndToEnd(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := writeServiceConfig(t, dataDir, "app-a", "app-b")
	provider := fake.New(nil) // any call would be recorded
	base, stop := startService(t, path, provider)

	code, tokenA, stderr := cli("token", "create", "--config", path, "--app", "app-a", "--scope", "jobs",
		"--expires", "30d", "--label", "ci")
	if code != exitOK || strings.Count(tokenA, "\n") != 1 || !strings.HasPrefix(tokenA, auth.Prefix) {
		t.Fatalf("token create = %d, stdout %q, stderr %q; want only the token on stdout", code, tokenA, stderr)
	}
	tokenA = strings.TrimSpace(tokenA)
	idA, secretA, _ := auth.Parse(tokenA)
	if !strings.Contains(stderr, idA) || strings.Contains(stderr, secretA) {
		t.Errorf("stderr %q: want the token id and never the secret", stderr)
	}
	if code, _, _ := cli("token", "create", "--config", path, "--app", "app-b", "--scope", "jobs:read", "--expires", "2099-01-01"); code != exitFailure {
		t.Errorf("token create beyond auth.max_token_ttl = %d, want %d", code, exitFailure)
	}
	_, tokenB, _ := cli("token", "create", "--config", path, "--app", "app-b", "--scope", "jobs:read", "--expires", "720h")
	tokenB = strings.TrimSpace(tokenB)

	// An expired token, written directly: the CLI refuses past expiries.
	store, err := job.Open(filepath.Join(dataDir, "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	expired, _, err := auth.Create(context.Background(), store, auth.Request{
		ApplicationID: "app-a", Scopes: []string{auth.ScopeJobs}, ExpiresAt: time.Now().Add(-time.Second),
	}, time.Hour, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	jobs := base + "/v1/jobs"
	if got := request(t, http.MethodGet, base+"/v1/quota", tokenA); got != http.StatusServiceUnavailable {
		t.Errorf("valid token: GET /v1/quota = %d, want the authenticated placeholder 503", got)
	}
	for _, tt := range []struct{ name, token string }{
		{"missing", ""},
		{"malformed", "vde_not_a_token"},
		{"wrong secret", tokenA[:len(tokenA)-4] + "AAAA"},
		{"expired", expired},
	} {
		if got := request(t, http.MethodPost, jobs, tt.token); got != http.StatusUnauthorized {
			t.Errorf("%s token: POST /v1/jobs = %d, want 401", tt.name, got)
		}
	}
	if got := request(t, http.MethodPost, jobs, tokenB); got != http.StatusForbidden {
		t.Errorf("jobs:read token: POST /v1/jobs = %d, want 403", got)
	}
	if got := request(t, http.MethodGet, jobs+"/j_1", tokenB); got != http.StatusServiceUnavailable {
		t.Errorf("jobs:read token: GET /v1/jobs/j_1 = %d, want the authenticated placeholder 503", got)
	}

	code, out, _ := cli("token", "list", "--config", path, "--app", "app-a")
	if code != exitOK || !strings.Contains(out, idA) || !strings.Contains(out, "active") || !strings.Contains(out, "ci") {
		t.Errorf("token list = %d %q, want token %s active with its label", code, out, idA)
	}
	if code, out, _ := cli("token", "revoke", idA, "--config", path); code != exitOK || !strings.Contains(out, "revoked") {
		t.Errorf("token revoke = %d %q", code, out)
	}
	if got := request(t, http.MethodGet, base+"/v1/quota", tokenA); got != http.StatusUnauthorized {
		t.Errorf("revoked token: GET /v1/quota = %d, want 401 at once", got)
	}
	if code, out, _ := cli("token", "revoke", "--config", path, idA); code != exitOK || !strings.Contains(out, "already revoked") {
		t.Errorf("second revoke = %d %q", code, out)
	}
	if code, _, _ := cli("token", "revoke", "--config", path, "zzzzzzzz"); code != exitFailure {
		t.Errorf("revoke of an unknown id = %d, want %d", code, exitFailure)
	}
	stop()

	// Restart without app-b: it is disabled and its token stops working.
	path = writeServiceConfig(t, dataDir, "app-a")
	base, stop = startService(t, path, provider)
	if got := request(t, http.MethodGet, base+"/v1/quota", tokenB); got != http.StatusUnauthorized {
		t.Errorf("token of a removed application: GET /v1/quota = %d, want 401", got)
	}
	code, out, _ = cli("app", "list", "--config", path)
	if code != exitOK || !strings.Contains(out, "app-a") || !regexpLine(out, `^app-b\s+-\s+false`) {
		t.Errorf("app list = %d %q, want app-a and a disabled app-b", code, out)
	}
	if code, _, stderr := cli("token", "create", "--config", path, "--app", "app-b", "--scope", "jobs", "--expires", "1d"); code != exitFailure || !strings.Contains(stderr, "disabled") {
		t.Errorf("token create for a disabled application = %d %q", code, stderr)
	}
	stop()

	if n := len(provider.Calls()); n != 0 {
		t.Errorf("provider called %d times, want 0", n)
	}
	store, err = job.Open(filepath.Join(dataDir, "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.ListExpired(context.Background(), time.Now().AddDate(100, 0, 0), 10)
	store.Close()
	if err != nil || len(stored) != 0 {
		t.Errorf("jobs stored = %d (%v), want none", len(stored), err)
	}

	checkAudit(t, filepath.Join(dataDir, "vde.db"), []string{
		"app.enabled app-a", "app.enabled app-b", "token.created app-a", "token.created app-b",
		"auth.failed ", "auth.failed ", "auth.failed app-a", "auth.failed app-a", "token.revoked app-a",
		"auth.failed app-a", "app.disabled app-b", "auth.failed app-b",
	})

	// The database never holds a token secret (job 02 acceptance).
	_, secretB, _ := auth.Parse(tokenB)
	_, secretExpired, _ := auth.Parse(expired)
	files, _ := filepath.Glob(filepath.Join(dataDir, "*"))
	if len(files) == 0 {
		t.Fatal("no database files")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{secretA, secretB, secretExpired} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s contains a token secret", filepath.Base(f))
			}
		}
	}
}

// checkAudit compares the audit trail's kind and application, in order, with want.
func checkAudit(t *testing.T, path string, want []string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT kind, application_id, details FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kind, app, details string
		if err := rows.Scan(&kind, &app, &details); err != nil {
			t.Fatal(err)
		}
		got = append(got, kind+" "+app)
		if strings.Contains(details, auth.Prefix) {
			t.Errorf("audit details %s carry a token", details)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("audit trail:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func regexpLine(text, pattern string) bool {
	return regexp.MustCompile("(?m)" + pattern).MatchString(text)
}

func TestParseExpires(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2027-10-16", time.Date(2027, 10, 16, 0, 0, 0, 0, time.UTC)},
		{"2027-10-16T08:30:00+02:00", time.Date(2027, 10, 16, 6, 30, 0, 0, time.UTC)},
		{"90d", now.Add(90 * 24 * time.Hour)},
		{"2160h", now.Add(2160 * time.Hour)},
		{"1h30m", now.Add(90 * time.Minute)},
	}
	for _, tt := range tests {
		got, err := parseExpires(tt.in, now)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("parseExpires(%q) = %s, %v; want %s", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "never", "-1h", "0d", "d", "2027-13-01", "16.10.2027"} {
		if _, err := parseExpires(bad, now); err == nil {
			t.Errorf("parseExpires(%q) accepted", bad)
		}
	}
}

func TestTokenUsage(t *testing.T) {
	t.Parallel()
	path := writeServiceConfig(t, t.TempDir(), "app-a")
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no subcommand", []string{"token"}, exitUsage},
		{"unknown subcommand", []string{"token", "rotate"}, exitUsage},
		{"create without expiry", []string{"token", "create", "--config", path, "--app", "app-a", "--scope", "jobs"}, exitUsage},
		{"create with unknown scope", []string{"token", "create", "--config", path, "--app", "app-a", "--scope", "admin", "--expires", "1d"}, exitUsage},
		{"create for an application serve never saw", []string{"token", "create", "--config", path, "--app", "app-a", "--scope", "jobs", "--expires", "1d"}, exitFailure},
		{"list without app", []string{"token", "list", "--config", path}, exitUsage},
		{"revoke without id", []string{"token", "revoke", "--config", path}, exitUsage},
		{"revoke with two ids", []string{"token", "revoke", "--config", path, "aaaaaaaa", "bbbbbbbb"}, exitUsage},
		{"app without list", []string{"app"}, exitUsage},
	}
	for _, tt := range tests {
		if code, _, stderr := cli(tt.args...); code != tt.want {
			t.Errorf("%s: exit %d, want %d; stderr %q", tt.name, code, tt.want, stderr)
		}
	}
}

func TestHealthcheck(t *testing.T) {
	t.Parallel()
	path := writeServiceConfig(t, t.TempDir(), "app-a")
	base, stop := startService(t, path, fake.New(nil))
	listen := strings.TrimPrefix(base, "http://")
	hcConfig := filepath.Join(t.TempDir(), "vde.yaml")
	if err := os.WriteFile(hcConfig, []byte("server: {listen: \""+listen+"\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, stderr := cli("healthcheck", "--config", hcConfig); code != exitOK || strings.TrimSpace(out) != "ok" {
		t.Errorf("healthcheck on a running service = %d %q %q, want 0 ok", code, out, stderr)
	}
	stop()
	if code, _, _ := cli("healthcheck", "--config", hcConfig); code != exitFailure {
		t.Errorf("healthcheck on a stopped service = %d, want %d", code, exitFailure)
	}
}

func TestHealthURL(t *testing.T) {
	t.Parallel()
	tls := config.TLS{CertFile: "c.pem", KeyFile: "k.pem"}
	tests := []struct {
		server config.Server
		want   string
	}{
		{config.Server{Listen: "127.0.0.1:8090"}, "http://127.0.0.1:8090/healthz"},
		{config.Server{Listen: ":8090"}, "http://127.0.0.1:8090/healthz"},
		{config.Server{Listen: "0.0.0.0:8090"}, "http://127.0.0.1:8090/healthz"},
		{config.Server{Listen: "[::]:8090"}, "http://127.0.0.1:8090/healthz"},
		{config.Server{Listen: "[::1]:8090"}, "http://[::1]:8090/healthz"},
		{config.Server{Listen: "10.0.0.5:8443", TLS: tls}, "https://10.0.0.5:8443/healthz"},
	}
	for _, tt := range tests {
		if got, err := healthURL(tt.server); err != nil || got != tt.want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", tt.server.Listen, got, err, tt.want)
		}
	}
	if _, err := healthURL(config.Server{Listen: "8090"}); err == nil {
		t.Error("healthURL accepted a listen address without a port")
	}
}

package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

func TestPublicAddr(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{
		"93.184.215.14": true, "2606:2800:21f:cb07:6820:80da:af6b:8b2c": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "fe80::1": false, "fc00::1": false, "0.0.0.0": false, "::": false,
		"100.64.0.1": false, "224.0.0.1": false, "64:ff9b::a00:1": false, "255.255.255.255": false,
		"0.1.2.3": false, "240.0.0.1": false, "::7f00:1": false, "2002:7f00:1::1": false, "2001:0:7f00:1::1": false,
		"2001:4860:4860::8888": true,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

// fetchRig is a submit rig with documents.fetch on, allowing a TLS test server on
// loopback (the production fetcher refuses loopback, checked below).
func fetchRig(t *testing.T, maxBytes int64) (*submitRig, *httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/doc.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(submitDocText)) })
	mux.HandleFunc("/same-host", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/doc.txt", http.StatusFound) })
	mux.HandleFunc("/other-host", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/doc.txt", http.StatusFound)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat("a", 4096))) })
	mux.HandleFunc("/missing", http.NotFound)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	fc := config.Fetch{Enabled: true, AllowedHosts: []string{host}, MaxBytes: maxBytes, Timeout: 5 * time.Second}
	rg := newSubmitRig(t, submitRigOptions{cfg: func(c *config.Config) { c.Documents.Fetch = fc }})
	tlsConfig := srv.Client().Transport.(*http.Transport).TLSClientConfig
	rg.s.fetch = newFetcher(fc, tlsConfig, func(a netip.Addr) bool { return a.IsLoopback() })
	return rg, srv, srv.URL
}

func urlBody(u string) map[string]any {
	m := jsonFrom(validParts())
	m["document"] = map[string]any{"media_type": "text/plain; charset=utf-8", "url": u}
	return m
}

func TestJSONSubmissionFetchesAnAllowedURL(t *testing.T) {
	t.Parallel()
	rg, _, base := fetchRig(t, 1024)
	for _, path := range []string{"/doc.txt", "/same-host"} {
		body := decodeJob(t, rg.submitJSON(t, "/v1/jobs", urlBody(base+path), nil))
		j, err := rg.db.GetJob(t.Context(), testApp, body["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if j.Size != int64(len(submitDocText)) || j.Representation != "text" {
			t.Errorf("%s: stored job = %+v", path, j)
		}
	}
}

func TestJSONSubmissionRefusesURLs(t *testing.T) {
	t.Parallel()
	rg, srv, base := fetchRig(t, 1024)
	u, _ := url.Parse(base)
	tests := []struct {
		name, url string
		status    int
		message   string
	}{
		{"plain http", "http://" + u.Host + "/doc.txt", 400, msgFetchRefused},
		{"host not allowed", "https://localhost:" + u.Port() + "/doc.txt", 400, msgFetchRefused},
		{"credentials", "https://user:pw@" + u.Host + "/doc.txt", 400, msgFetchRefused},
		{"redirect to another host", base + "/other-host", 400, msgFetchRefused},
		{"over max_bytes", base + "/big", 413, msgFetchedTooLarge},
		{"not 200", base + "/missing", 400, msgFetchFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := rg.submitJSON(t, "/v1/jobs", urlBody(tt.url), nil)
			if w.Code != tt.status || decodeError(t, w).Message != tt.message {
				t.Errorf("= %d %s, want %d %q", w.Code, w.Body.String(), tt.status, tt.message)
			}
		})
	}
	// The production address check refuses the loopback test server even though its
	// host is allowed: SSRF to the service's own network is refused after resolution.
	rg.s.fetch = newFetcher(config.Fetch{Enabled: true, AllowedHosts: []string{u.Host}, Timeout: 5 * time.Second},
		srv.Client().Transport.(*http.Transport).TLSClientConfig, nil)
	w := rg.submitJSON(t, "/v1/jobs", urlBody(base+"/doc.txt"), nil)
	if w.Code != http.StatusBadRequest || decodeError(t, w).Message != msgFetchFailed {
		t.Errorf("loopback with the production check = %d %s, want 400", w.Code, w.Body.String())
	}
	if rg.quotaUsed(t) != 0 {
		t.Errorf("quota used = %d, want 0", rg.quotaUsed(t))
	}
}

// TestSubmitLimitsBeforeCostlyWork: at the application's in-progress ceiling a
// submission is refused before its document url is fetched or its schema compiled; a
// replay of a used Idempotency-Key still answers the existing job (security review F4).
func TestSubmitLimitsBeforeCostlyWork(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(submitDocText))
	}))
	t.Cleanup(srv.Close)
	fc := config.Fetch{Enabled: true, AllowedHosts: []string{strings.TrimPrefix(srv.URL, "https://")}, MaxBytes: 1024, Timeout: 5 * time.Second}
	rg := newSubmitRig(t, submitRigOptions{
		cfg: func(c *config.Config) { c.Documents.Fetch = fc },
		app: func(a *job.Application) { a.MaxInProgress = 1 },
	})
	rg.s.fetch = newFetcher(fc, srv.Client().Transport.(*http.Transport).TLSClientConfig, func(a netip.Addr) bool { return a.IsLoopback() })
	key := idempotencyKey("at-ceiling")
	first := decodeJob(t, rg.submitJSON(t, "/v1/jobs", urlBody(srv.URL+"/doc.txt"), key)) // the one job in progress
	if hits.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", hits.Load())
	}

	tests := []struct {
		name string
		send func() *httptest.ResponseRecorder
	}{
		{"document url", func() *httptest.ResponseRecorder {
			return rg.submitJSON(t, "/v1/jobs", urlBody(srv.URL+"/doc.txt"), nil)
		}},
		{"invalid schema", func() *httptest.ResponseRecorder {
			return rg.submit(t, withPart(validParts(), mpart{name: partSchema, ctype: "application/schema+json", data: []byte(`{"type": 12}`)}), nil)
		}},
	}
	for _, tt := range tests {
		w := tt.send()
		if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeRateLimited {
			t.Errorf("%s at the ceiling: %d %s, want 429 rate_limited before any other check", tt.name, w.Code, w.Body)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("fetches = %d after refused submissions, want 1", hits.Load())
	}
	w := rg.submitJSON(t, "/v1/jobs", urlBody(srv.URL+"/doc.txt"), key)
	if body := decodeJob(t, w); body["id"] != first["id"] || w.Header().Get(headerIdempotencyReplayed) != "true" {
		t.Errorf("replay at the ceiling: %d %s, want the existing job", w.Code, w.Body)
	}
}

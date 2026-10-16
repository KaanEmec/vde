package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/auth"
)

func TestGetQuota(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, false)
	h := handlerOf(s)
	token := testToken(t, s, auth.ScopeJobsRead)
	ctx := context.Background()
	windowStart := time.Now().UTC().Truncate(24 * time.Hour)
	for range 3 { // three submissions charged in today's window
		if _, err := s.deps.Store.IncrementQuota(ctx, testApp, windowStart, 10); err != nil {
			t.Fatal(err)
		}
	}
	get := func() (quotaBody, http.Header) {
		t.Helper()
		w := serve(h, http.MethodGet, "/v1/quota", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("GET /v1/quota = %d %s", w.Code, w.Body)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || len(raw) != 4 {
			t.Fatalf("body %s: want exactly limit, remaining, reset, window", w.Body)
		}
		var q quotaBody
		if err := json.Unmarshal(w.Body.Bytes(), &q); err != nil {
			t.Fatal(err)
		}
		return q, w.Header()
	}
	q, hdr := get()
	want := quotaBody{Limit: 10, Remaining: 7, Reset: windowStart.Add(24 * time.Hour).Format(time.RFC3339), Window: "P1D"}
	if q != want {
		t.Errorf("body = %+v, want %+v", q, want)
	}
	if hdr.Get("X-Quota-Remaining") != strconv.Itoa(q.Remaining) || hdr.Get("X-Quota-Reset") != q.Reset {
		t.Errorf("headers %q %q differ from the body %+v", hdr.Get("X-Quota-Remaining"), hdr.Get("X-Quota-Reset"), q)
	}
	if hdr.Get(headerServiceVersion) != "" {
		t.Error("X-Service-Version outside /v1/jobs")
	}
	for range 100 {
		get()
	}
	if again, _ := get(); again != want {
		t.Errorf("after 100 reads: %+v, want unchanged %+v", again, want)
	}
	if used, err := s.deps.Store.GetQuota(ctx, testApp, windowStart); err != nil || used != 3 {
		t.Errorf("window count = %d (%v), want 3: quota reads must not charge", used, err)
	}
	if w := serve(h, http.MethodGet, "/v1/quota", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("without a token: %d, want 401", w.Code)
	}
}

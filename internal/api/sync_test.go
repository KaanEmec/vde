package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// postTo sends a POST to target with the rig's token.
func (rg *submitRig) postTo(target string, body []byte, contentType string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Authorization", "Bearer "+rg.token)
	r.Header.Set("Content-Type", contentType)
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	rg.h.ServeHTTP(w, r)
	return w
}

func (rg *submitRig) extract(t *testing.T, parts []mpart, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, parts)
	return rg.postTo("/v1/extract", body, ct, mod)
}

func syncBody(t *testing.T, w *httptest.ResponseRecorder, status int) jobResponse {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body %s", w.Code, status, w.Body.String())
	}
	var j jobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if loc := w.Header().Get("Location"); loc != "/v1/jobs/"+j.ID {
		t.Errorf("Location = %q, want /v1/jobs/%s", loc, j.ID)
	}
	return j
}

// The acceptance criterion: a text document returns 200 accepted in one request with
// the fake provider, charged to the quota like a job.
func TestExtractSyncReturnsTheTerminalJob(t *testing.T) {
	t.Parallel()
	for _, notify := range []bool{true, false} { // false: the store poll alone (another replica)
		rg := newSubmitRig(t, submitRigOptions{pool: true, notify: notify})
		start := time.Now()
		w := rg.extract(t, validParts(), nil)
		j := syncBody(t, w, http.StatusOK)
		if j.Status != job.StatusAccepted || string(j.Result) != submitCandidate || j.FinishedAt == nil || j.Attempts != 1 {
			t.Errorf("notify %v: job = %+v", notify, j)
		}
		if notify && time.Since(start) > 900*time.Millisecond {
			t.Errorf("notified wait took %s, want well under the poll interval", time.Since(start))
		}
		if w.Header().Get(headerServiceVersion) != testServiceVersion || w.Header().Get(headerQuotaRemaining) != "99" {
			t.Errorf("headers = %v", w.Header())
		}
		if rg.quotaUsed(t) != 1 {
			t.Errorf("quota used = %d, want 1", rg.quotaUsed(t))
		}
	}
}

func TestExtractSyncFallsBackTo202(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{
		pool: true, notify: true,
		cfg: func(c *config.Config) { c.Sync.MaxWait = 150 * time.Millisecond },
		ext: fake.Script{{Answer: submitCandidate, Delay: 2 * time.Second, Repeat: true}},
	})
	j := syncBody(t, rg.extract(t, validParts(), nil), http.StatusAccepted)
	if j.Status.Terminal() || j.FinishedAt != nil {
		t.Errorf("job = %+v, want queued or running", j)
	}
	if got := rg.waitTerminal(t, j.ID); got.Status != job.StatusAccepted {
		t.Errorf("job ran on to %s, want accepted", got.Status)
	}
}

// A retention: none job delivered by POST /v1/extract has had its first read.
func TestExtractSyncIsTheFirstReadOfRetentionNone(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{pool: true, notify: true})
	parts := withPart(validParts(), mpart{name: partRetention, ctype: "text/plain", data: []byte("none")})
	j := syncBody(t, rg.extract(t, parts, nil), http.StatusOK)
	if string(j.Result) != submitCandidate {
		t.Errorf("result = %s", j.Result)
	}
	if w := rg.get("/v1/jobs/" + j.ID); w.Code != http.StatusNotFound {
		t.Errorf("GET after the sync read = %d, want 404", w.Code)
	}
}

// The in-progress ceiling and the refusals are the submit path's.
func TestExtractSyncSharesTheSubmitLimits(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{app: func(a *job.Application) { a.MaxInProgress = 1 }})
	decodeJob(t, rg.submit(t, validParts(), nil)) // no pool: stays queued
	w := rg.extract(t, validParts(), nil)
	if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeRateLimited || w.Header().Get("Retry-After") == "" {
		t.Errorf("over the in-progress ceiling = %d %s", w.Code, w.Body.String())
	}
	w = rg.extract(t, withoutPart(validParts(), partDocument), nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("without a document = %d, want 400", w.Code)
	}
	if rg.quotaUsed(t) != 1 {
		t.Errorf("quota used = %d, want 1 (refusals are not charged)", rg.quotaUsed(t))
	}
}

// Over a real connection the server's ReadTimeout and WriteTimeout are shorter than the
// wait; the handler extends both, so the client gets its 200.
func TestExtractSyncOutlivesServerTimeouts(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{
		pool: true, notify: true,
		ext: fake.Script{{Answer: submitCandidate, Delay: 700 * time.Millisecond, Repeat: true}},
	})
	srv := httptest.NewUnstartedServer(rg.h)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	srv.Start()
	defer srv.Close()
	body, ct := multipartBody(t, validParts())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/v1/extract", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+rg.token)
	req.Header.Set("Content-Type", ct)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data)
	}
}

// TestExtractSyncExtracted: under judge policy never the synchronous endpoint returns the
// terminal extracted job with its verification, like the job resource (ADR 0009).
func TestExtractSyncExtracted(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{pool: true, notify: true,
		app: func(a *job.Application) { a.JudgePolicy = "never" }})
	j := syncBody(t, rg.extract(t, validParts(), nil), http.StatusOK)
	if j.Status != job.StatusExtracted || j.Verification == nil || *j.Verification != job.VerificationNone || string(j.Result) != submitCandidate {
		t.Errorf("job = %+v, want extracted with verification none", j)
	}
	if len(rg.judge.Calls()) != 0 {
		t.Errorf("the judge was called %d times", len(rg.judge.Calls()))
	}
}

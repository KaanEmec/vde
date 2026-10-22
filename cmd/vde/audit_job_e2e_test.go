package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/provider/fake"
)

// TestFailureCodeAfterRetentionNone: a retention: none job fails with the fake provider
// down, its terminal read is the first (and last: the job is 404 after it, contract
// §10), and its error code is still found by job id, with vde audit list --job and
// GET /admin/v1/audit?job_id= (contract §12).
func TestFailureCodeAfterRetentionNone(t *testing.T) {
	t.Parallel()
	path := writeServiceConfig(t, t.TempDir(), "app-a")
	base, admin, stop := startServe(t, path, serveOptions{build: serveWith(fake.New(fake.AlwaysRetryable(503))), admin: true})
	token := jobsToken(t, path, "app-a")
	code, adminToken, stderr := cli("token", "create", "--config", path, "--admin", "--expires", "1d")
	if code != exitOK {
		t.Fatalf("token create --admin = %d %q", code, stderr)
	}
	adminToken = strings.TrimSpace(adminToken)

	form, contentType := submission(t)
	r := call(t, http.MethodPost, base+"/v1/jobs", token, form, contentType)
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(r.body), &created); err != nil || created.ID == "" || r.status >= 300 {
		t.Fatalf("submit = %d %s", r.status, r.body)
	}
	_, final := waitJob(t, base, token, created.ID, 30*time.Second)
	if string(final["status"]) != `"failed"` || !strings.Contains(string(final["error"]), `"provider_unavailable"`) {
		t.Fatalf("terminal job = %v", final)
	}
	if r := call(t, http.MethodGet, base+"/v1/jobs/"+created.ID, token, nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("second terminal read = %d, want 404", r.status)
	}

	// An unrelated listing does not find the job; --job does, without any --since.
	code, out, stderr := cli("audit", "list", "--config", path, "--job", created.ID)
	if code != exitOK {
		t.Fatalf("audit list --job = %d %q", code, stderr)
	}
	for _, want := range []string{"job.created", "job.finished", "job.deleted", created.ID, `"error_code":"provider_unavailable"`} {
		if !strings.Contains(out, want) {
			t.Errorf("audit list --job lacks %s:\n%s", want, out)
		}
	}
	if code, out, _ := cli("audit", "list", "--config", path, "--job", "j_unknown"); code != exitOK || strings.Count(out, "\n") != 1 {
		t.Errorf("audit list --job of an unknown job = %d %q, want the header only", code, out)
	}

	r = call(t, http.MethodGet, admin+"/admin/v1/audit?job_id="+created.ID+"&kind=job.finished", adminToken, nil, "")
	if r.status != http.StatusOK || !strings.Contains(r.body, `"error_code":"provider_unavailable"`) || !strings.Contains(r.body, created.ID) {
		t.Errorf("GET /admin/v1/audit?job_id= = %d %s", r.status, r.body)
	}
	stop()
}

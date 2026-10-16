package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/provider/fake"
)

// TestAdminEndToEnd runs vde serve with the admin listener: an admin token from the CLI
// creates a dynamic application and its token over HTTP, the token submits a job, the
// admin API lists it as metadata and force-deletes it, the scopes never cross between
// /v1 and /admin/v1, the dynamic application survives a restart, and vde audit list
// shows the admin's changes.
func TestAdminEndToEnd(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := writeServiceConfig(t, dataDir, "app-a")
	script, err := fake.Load(filepath.Join(scenarioDir, "accept-first-try.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	base, admin, stop := startServiceAdmin(t, path, script, true)

	for _, args := range [][]string{
		{"token", "create", "--config", path, "--admin", "--app", "app-a", "--expires", "1d"},
		{"token", "create", "--config", path, "--admin", "--scope", "jobs", "--expires", "1d"},
		{"token", "create", "--config", path, "--admin"},
		{"token", "list", "--config", path, "--admin", "--app", "app-a"},
	} {
		if code, _, _ := cli(args...); code != exitUsage {
			t.Errorf("%v = %d, want usage error", args[1:], code)
		}
	}
	code, adminToken, stderr := cli("token", "create", "--config", path, "--admin", "--expires", "1d", "--label", "ops")
	if code != exitOK || !strings.Contains(stderr, "admin API") {
		t.Fatalf("token create --admin = %d %q", code, stderr)
	}
	adminToken = strings.TrimSpace(adminToken)
	adminID := adminToken[4:12]
	if code, out, _ := cli("token", "list", "--config", path, "--admin"); code != exitOK || !strings.Contains(out, adminID) {
		t.Errorf("token list --admin = %d %q", code, out)
	}

	send := func(method, url, token, body string) reply {
		t.Helper()
		var r reply
		if body == "" {
			r = call(t, method, url, token, nil, "")
		} else {
			r = call(t, method, url, token, strings.NewReader(body), "application/json")
		}
		return r
	}
	if r := send(http.MethodPost, admin+"/admin/v1/applications", adminToken, `{"id": "dyn", "quota": {"limit": 5}}`); r.status != http.StatusCreated {
		t.Fatalf("create application = %d %s", r.status, r.body)
	}
	r := send(http.MethodPost, admin+"/admin/v1/applications/dyn/tokens", adminToken, `{"scopes": ["jobs"], "expires": "1d"}`)
	var created struct{ Token, ID string }
	if r.status != http.StatusCreated || json.Unmarshal([]byte(r.body), &created) != nil || created.Token == "" {
		t.Fatalf("create token = %d %s", r.status, r.body)
	}
	appToken := created.Token

	// Scopes never cross.
	if r := send(http.MethodGet, base+"/v1/quota", adminToken, ""); r.status != http.StatusForbidden {
		t.Errorf("admin token on /v1/quota = %d, want 403", r.status)
	}
	if r := send(http.MethodGet, admin+"/admin/v1/applications", appToken, ""); r.status != http.StatusForbidden {
		t.Errorf("application token on the admin API = %d, want 403", r.status)
	}
	if r := send(http.MethodGet, admin+"/v1/quota", appToken, ""); r.status != http.StatusNotFound {
		t.Errorf("/v1 on the admin listener = %d, want 404", r.status)
	}
	if r := send(http.MethodGet, base+"/v1/quota", appToken, ""); r.status != http.StatusOK || r.header.Get("X-Quota-Remaining") != "5" {
		t.Errorf("dynamic application's quota = %d %s", r.status, r.body)
	}

	form, contentType := submission(t)
	r = call(t, http.MethodPost, base+"/v1/jobs", appToken, form, contentType)
	var job struct{ ID string }
	if r.status != http.StatusAccepted || json.Unmarshal([]byte(r.body), &job) != nil {
		t.Fatalf("submit = %d %s", r.status, r.body)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r = send(http.MethodGet, admin+"/admin/v1/jobs/"+job.ID, adminToken, "")
		if strings.Contains(r.body, `"status":"accepted"`) || time.Now().After(deadline) {
			break
		}
	}
	if r.status != http.StatusOK || !strings.Contains(r.body, `"status":"accepted"`) || strings.Contains(r.body, "Ada") {
		t.Errorf("admin job = %d %s, want accepted metadata without content", r.status, r.body)
	}
	r = send(http.MethodGet, admin+"/admin/v1/jobs?app=dyn", adminToken, "")
	if !strings.Contains(r.body, job.ID) || strings.Contains(r.body, "Ada") || strings.Contains(r.body, "result") {
		t.Errorf("admin job list = %s", r.body)
	}
	if r := send(http.MethodDelete, admin+"/admin/v1/jobs/"+job.ID, adminToken, ""); r.status != http.StatusNoContent {
		t.Errorf("force delete = %d %s", r.status, r.body)
	}
	if r := send(http.MethodGet, base+"/v1/jobs/"+job.ID, appToken, ""); r.status != http.StatusNotFound {
		t.Errorf("force-deleted job on /v1 = %d, want 404", r.status)
	}
	stop()

	// The dynamic application persists; the YAML ones are synced as before.
	base, _, stop = startServiceAdmin(t, path, script, true)
	if r := send(http.MethodGet, base+"/v1/quota", appToken, ""); r.status != http.StatusOK {
		t.Errorf("dynamic application after a restart = %d, want 200", r.status)
	}
	stop()
	if code, out, _ := cli("app", "list", "--config", path); code != exitOK || !regexpLine(out, `^dyn\s+-\s+admin\s+true`) {
		t.Errorf("app list = %d %q, want dyn from admin, enabled", code, out)
	}
	code, out, stderr := cli("audit", "list", "--config", path, "--app", "dyn")
	if code != exitOK {
		t.Fatalf("audit list = %d %q", code, stderr)
	}
	for _, want := range []string{"app.enabled", "token.created", "job.created", "job.finished", "job.deleted", "admin:" + adminID, `"reason":"admin"`} {
		if !strings.Contains(out, want) {
			t.Errorf("audit list lacks %s:\n%s", want, out)
		}
	}
	if code, out, _ := cli("audit", "list", "--config", path, "--kind", "token.created", "--limit", "1"); code != exitOK || strings.Count(out, "token.created") != 1 {
		t.Errorf("audit list --kind --limit = %d %q", code, out)
	}
	if code, _, _ := cli("audit", "list", "--config", path, "--kind", "admin.request"); code != exitUsage {
		t.Errorf("audit list with an unknown kind = %d, want usage error", code)
	}
}

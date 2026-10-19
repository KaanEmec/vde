package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// adminRig is the admin handler and the /v1 handler on one store, with an admin token.
type adminRig struct {
	admin, v1 http.Handler
	store     *auditStore
	blobs     blob.Store
	token     string // admin token
	tokenID   string
	now       time.Time
}

func newAdminRig(t *testing.T) *adminRig {
	t.Helper()
	cfg, err := config.Parse([]byte(submitTestConfig), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, testApp)
	blobs, err := blob.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rg := &adminRig{store: store, blobs: blobs, now: time.Now()}
	deps := Deps{
		Config: cfg, Store: store, Blobs: blobs, ServiceVersion: testServiceVersion,
		Logger: slog.New(slog.DiscardHandler), Now: func() time.Time { return rg.now },
	}
	rg.admin, rg.v1 = NewAdmin(deps), New(deps)
	token, row, err := auth.Create(t.Context(), store, auth.Request{
		Scopes: []string{auth.ScopeAdmin}, ExpiresAt: rg.now.Add(7 * 24 * time.Hour),
	}, 30*24*time.Hour, rg.now)
	if err != nil {
		t.Fatal(err)
	}
	rg.token, rg.tokenID = token, row.ID
	return rg
}

// do sends a request with token (the admin token when "-") and a JSON body.
func do(h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	return serve(h, method, target, func(r *http.Request) {
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			r.Body = httpBody(body)
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Type", "application/json")
		}
	})
}

func (rg *adminRig) do(t *testing.T, method, target, body string, want int) map[string]any {
	t.Helper()
	w := do(rg.admin, method, target, rg.token, body)
	if w.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, target, w.Code, w.Body, want)
	}
	out := map[string]any{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: body %s: %v", method, target, w.Body, err)
		}
	}
	return out
}

// lastAudit returns the last audit event of kind.
func (rg *adminRig) lastAudit(kind audit.Kind) job.AuditEvent {
	rg.store.mu.Lock()
	defer rg.store.mu.Unlock()
	for i := len(rg.store.events) - 1; i >= 0; i-- {
		if rg.store.events[i].Kind == string(kind) {
			return rg.store.events[i]
		}
	}
	return job.AuditEvent{}
}

func TestAdminScopes(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	appToken := issueToken(t, rg.store, testApp, []string{auth.ScopeJobs}, time.Now().Add(time.Hour))
	for _, rt := range newServerWith(testDeps(t, Deps{}), true).adminRoutes() {
		method, path, _ := strings.Cut(rt.pattern, " ")
		path = strings.ReplaceAll(strings.ReplaceAll(path, "{id}", testApp), "{$}", "")
		if w := do(rg.admin, method, path, appToken, ""); w.Code != http.StatusForbidden || decodeError(t, w).Code != codeForbidden {
			t.Errorf("application token on %s = %d, want 403", rt.pattern, w.Code)
		}
		if w := do(rg.admin, method, path, "", ""); w.Code != http.StatusUnauthorized {
			t.Errorf("no token on %s = %d, want 401", rt.pattern, w.Code)
		}
	}
	for _, target := range []string{"/v1/quota", "/v1/openapi.yaml", "/v1/jobs/j_x"} {
		w := do(rg.v1, http.MethodGet, target, rg.token, "")
		if w.Code != http.StatusForbidden || w.Header().Get(headerQuotaRemaining) != "" {
			t.Errorf("admin token on GET %s = %d (quota header %q), want 403 without quota headers",
				target, w.Code, w.Header().Get(headerQuotaRemaining))
		}
	}
	if w := do(rg.v1, http.MethodPost, "/v1/jobs", rg.token, "{}"); w.Code != http.StatusForbidden {
		t.Errorf("admin token submitting = %d, want 403", w.Code)
	}
	if w := do(rg.v1, http.MethodGet, "/admin/v1/applications", rg.token, ""); w.Code != http.StatusNotFound {
		t.Errorf("admin route on the /v1 listener = %d, want 404", w.Code)
	}
	if w := do(rg.admin, http.MethodGet, "/v1/quota", appToken, ""); w.Code != http.StatusNotFound {
		t.Errorf("/v1 route on the admin listener = %d, want 404", w.Code)
	}
	rg.do(t, http.MethodGet, "/admin/v1/applications", "", http.StatusOK)
	if e := rg.lastAudit(audit.AuthFailedKind); e.Kind == "" {
		t.Error("a refused admin request wrote no auth.failed event")
	}
}

func TestAdminApplications(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	created := rg.do(t, http.MethodPost, "/admin/v1/applications",
		`{"id": "dyn-1", "name": "Dynamic", "rate": {"requests_per_minute": 5}, "retention": {"default": "none"}}`,
		http.StatusCreated)
	want := map[string]any{"id": "dyn-1", "name": "Dynamic", "enabled": true, "source": "admin", "max_attempts": 3.0}
	for k, v := range want {
		if created[k] != v {
			t.Errorf("created %s = %v, want %v", k, created[k], v)
		}
	}
	quota, _ := created["quota"].(map[string]any)
	rate, _ := created["rate"].(map[string]any)
	if quota["limit"] != 1000.0 || quota["window"] != "P1D" || rate["requests_per_minute"] != 5.0 || rate["submits_per_minute"] != 30.0 {
		t.Errorf("defaults not applied: quota %v rate %v", quota, rate)
	}
	if e := rg.lastAudit(audit.AppEnabledKind); e.ApplicationID != "dyn-1" || e.Actor != "admin:"+rg.tokenID {
		t.Errorf("app.enabled = %+v, want dyn-1 by admin:%s", e, rg.tokenID)
	}

	for _, tt := range []struct{ body, field string }{
		{`{"id": "dyn-1"}`, ""},
		{`{"id": "Bad Id"}`, "id"},
		{`{"id": "dyn-2", "quota": {"window": "P1M"}}`, "quota.window"},
		{`{"id": "dyn-2", "rate": {"max_in_progress": -1}}`, "rate.max_in_progress"},
		{`{"id": "dyn-2", "colour": "red"}`, ""},
	} {
		w := do(rg.admin, http.MethodPost, "/admin/v1/applications", rg.token, tt.body)
		e := decodeError(t, w)
		switch {
		case tt.body == `{"id": "dyn-1"}`:
			if w.Code != http.StatusConflict || e.Code != codeConflict {
				t.Errorf("duplicate id = %d %s, want 409 conflict", w.Code, e.Code)
			}
		case w.Code != http.StatusBadRequest || !strings.Contains(e.Message, tt.field):
			t.Errorf("POST %s = %d %q, want 400 naming %q", tt.body, w.Code, e.Message, tt.field)
		}
	}

	patched := rg.do(t, http.MethodPatch, "/admin/v1/applications/dyn-1", `{"quota": {"limit": 7}, "enabled": false}`, http.StatusOK)
	quota, _ = patched["quota"].(map[string]any)
	rate, _ = patched["rate"].(map[string]any)
	if patched["enabled"] != false || quota["limit"] != 7.0 || quota["window"] != "P1D" || rate["requests_per_minute"] != 5.0 || patched["name"] != "Dynamic" {
		t.Errorf("PATCH changed more or less than the fields sent: %v", patched)
	}
	if e := rg.lastAudit(audit.AppDisabledKind); e.ApplicationID != "dyn-1" {
		t.Errorf("app.disabled = %+v", e)
	}
	if w := do(rg.admin, http.MethodPatch, "/admin/v1/applications/dyn-1", rg.token, `{"id": "other"}`); w.Code != http.StatusBadRequest {
		t.Errorf("PATCH renaming the id = %d, want 400", w.Code)
	}
	if w := do(rg.admin, http.MethodGet, "/admin/v1/applications/nope", rg.token, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown application = %d, want 404", w.Code)
	}
	got := rg.do(t, http.MethodGet, "/admin/v1/applications/dyn-1", "", http.StatusOK)
	if got["enabled"] != false {
		t.Errorf("GET after PATCH = %v", got)
	}
	list := rg.do(t, http.MethodGet, "/admin/v1/applications", "", http.StatusOK)
	if apps, _ := list["applications"].([]any); len(apps) != 2 {
		t.Errorf("list = %v, want %s and dyn-1", list, testApp)
	}
	q := rg.do(t, http.MethodGet, "/admin/v1/applications/dyn-1/quota", "", http.StatusOK)
	if q["limit"] != 7.0 || q["used"] != 0.0 || q["remaining"] != 7.0 || q["window"] != "P1D" || q["reset"] == "" {
		t.Errorf("quota = %v", q)
	}
}

// TestAdminApplicationJudge: an application's judge settings are created, patched and
// read over the admin API and validated like applications[].judge.
func TestAdminApplicationJudge(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	judgeOf := func(app map[string]any) map[string]any {
		j, _ := app["judge"].(map[string]any)
		return j
	}
	plain := rg.do(t, http.MethodPost, "/admin/v1/applications", `{"id": "plain"}`, http.StatusCreated)
	if j := judgeOf(plain); j["policy"] != "" || j["allow_override"] != false || j["sample_rate"] != nil {
		t.Errorf("default judge = %v, want the role's policy, no override, default rate", j)
	}
	created := rg.do(t, http.MethodPost, "/admin/v1/applications",
		`{"id": "lab", "judge": {"policy": "auto", "allow_override": true, "sample_rate": 0.25}}`, http.StatusCreated)
	if j := judgeOf(created); j["policy"] != "auto" || j["allow_override"] != true || j["sample_rate"] != 0.25 {
		t.Errorf("created judge = %v", j)
	}
	row, err := rg.store.GetApplication(t.Context(), "lab")
	if err != nil || row.JudgePolicy != "auto" || !row.JudgeAllowOverride || row.JudgeSampleRate == nil || *row.JudgeSampleRate != 0.25 {
		t.Errorf("stored = %+v, %v", row, err)
	}
	patched := rg.do(t, http.MethodPatch, "/admin/v1/applications/lab", `{"judge": {"policy": "never"}}`, http.StatusOK)
	if j := judgeOf(patched); j["policy"] != "never" || j["allow_override"] != true || j["sample_rate"] != 0.25 {
		t.Errorf("PATCH policy only = %v, want the other judge fields kept", j)
	}
	if j := judgeOf(rg.do(t, http.MethodGet, "/admin/v1/applications/lab", "", http.StatusOK)); j["policy"] != "never" {
		t.Errorf("GET after PATCH = %v", j)
	}
	for _, tt := range []struct{ method, path, body, field string }{
		{http.MethodPost, "/admin/v1/applications", `{"id": "bad-1", "judge": {"policy": "sometimes"}}`, "judge.policy"},
		{http.MethodPost, "/admin/v1/applications", `{"id": "bad-2", "judge": {"sample_rate": 1.5}}`, "judge.sample_rate"},
		{http.MethodPatch, "/admin/v1/applications/lab", `{"judge": {"sample_rate": -1}}`, "judge.sample_rate"},
		{http.MethodPatch, "/admin/v1/applications/lab", `{"judge": {"allow_override": "yes"}}`, ""},
	} {
		w := do(rg.admin, tt.method, tt.path, rg.token, tt.body)
		if e := decodeError(t, w); w.Code != http.StatusBadRequest || !strings.Contains(e.Message, tt.field) {
			t.Errorf("%s %s = %d %q, want 400 naming %q", tt.method, tt.body, w.Code, e.Message, tt.field)
		}
	}
	if row, _ := rg.store.GetApplication(t.Context(), "lab"); row.JudgePolicy != "never" || *row.JudgeSampleRate != 0.25 {
		t.Errorf("a refused PATCH changed the row: %+v", row)
	}
}

func TestAdminTokens(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	created := rg.do(t, http.MethodPost, "/admin/v1/applications/"+testApp+"/tokens",
		`{"scopes": ["jobs:read"], "expires": "1h", "label": "ops"}`, http.StatusCreated)
	token, _ := created["token"].(string)
	id, _, err := auth.Parse(token)
	if err != nil || created["id"] != id || created["status"] != "active" || created["label"] != "ops" {
		t.Fatalf("created token = %v", created)
	}
	if w := do(rg.v1, http.MethodGet, "/v1/quota", token, ""); w.Code != http.StatusOK {
		t.Errorf("new token on /v1/quota = %d, want 200", w.Code)
	}
	if e := rg.lastAudit(audit.TokenCreatedKind); e.ApplicationID != testApp || !strings.Contains(string(e.Details), id) {
		t.Errorf("token.created = %+v", e)
	}
	listed := rg.do(t, http.MethodGet, "/admin/v1/applications/"+testApp+"/tokens", "", http.StatusOK)
	tokens, _ := listed["tokens"].([]any)
	if len(tokens) != 1 {
		t.Fatalf("tokens = %v", listed)
	}
	if _, ok := tokens[0].(map[string]any)["token"]; ok || strings.Contains(fmtJSON(listed), token[len(token)-20:]) {
		t.Error("the token list carries the secret")
	}
	for _, body := range []string{
		`{"scopes": ["admin"], "expires": "1h"}`,
		`{"scopes": [], "expires": "1h"}`,
		`{"scopes": ["jobs"], "expires": "-1h"}`,
		`{"scopes": ["jobs"], "expires": "3h"}`, // within auth.max_token_ttl (two years by default)
	} {
		w := do(rg.admin, http.MethodPost, "/admin/v1/applications/"+testApp+"/tokens", rg.token, body)
		if body == `{"scopes": ["jobs"], "expires": "3h"}` {
			if w.Code != http.StatusCreated {
				t.Errorf("POST %s = %d, want 201", body, w.Code)
			}
			continue
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", body, w.Code)
		}
	}

	rg.do(t, http.MethodDelete, "/admin/v1/tokens/"+id, "", http.StatusNoContent)
	rg.do(t, http.MethodDelete, "/admin/v1/tokens/"+id, "", http.StatusNoContent) // again: still 204
	if w := do(rg.v1, http.MethodGet, "/v1/quota", token, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token = %d, want 401", w.Code)
	}
	if e := rg.lastAudit(audit.TokenRevokedKind); !strings.Contains(string(e.Details), id) || e.Actor != "admin:"+rg.tokenID {
		t.Errorf("token.revoked = %+v", e)
	}
	if n := countKind(rg, audit.TokenRevokedKind); n != 1 {
		t.Errorf("%d token.revoked events, want 1 (the second DELETE changes nothing)", n)
	}
	if w := do(rg.admin, http.MethodDelete, "/admin/v1/tokens/zzzzzzzz", rg.token, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown token = %d, want 404", w.Code)
	}
}

// adminJobKeys is the complete key set of a job in the admin API: metadata only.
var adminJobKeys = []string{
	"application_id", "attempts", "bytes", "content_deleted_at", "created_at", "error_code", "expires_at",
	"finished_at", "id", "input_tokens", "inputs_deleted_at", "judge_policy", "max_attempts", "media_type",
	"output_tokens", "pages", "representation", "retention", "service_version", "started_at", "status",
	"verification",
}

func TestAdminJobs(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	ctx := t.Context()
	const sentinel = "SENTINEL-CONTENT-7d3f"
	var ids []string
	for i := range 3 {
		j := &job.Job{
			ApplicationID: testApp, ServiceVersion: testServiceVersion, Representation: "text",
			MediaType: "text/plain", Size: 10, ExtractorInstructions: sentinel, JudgeInstructions: sentinel,
			Schema: `{"description": "` + sentinel + `"}`, IdempotencyKey: sentinel + string(rune('a'+i)),
			Attachments: []job.Attachment{{Name: sentinel + ".pdf", MediaType: "application/pdf", Bytes: 1}},
			CreatedAt:   rg.now.Add(time.Duration(i) * time.Minute),
		}
		if err := rg.store.CreateJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		if err := rg.blobs.Put(ctx, j.ID, blob.Document, strings.NewReader(sentinel)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	// The first job ran and failed with feedback and a result.
	leased, err := rg.store.LeaseNextQueued(ctx, "w", rg.now.Add(time.Hour), rg.now.Add(2*time.Hour), nil)
	if err != nil || leased.ID != ids[0] {
		t.Fatalf("lease = %s, %v", leased.ID, err)
	}
	if err := rg.store.Finish(ctx, ids[0], "w", job.Outcome{
		Status: job.StatusFailed, Attempts: 1, Result: json.RawMessage(`{"x": "` + sentinel + `"}`),
		Feedback: json.RawMessage(`[{"message": "` + sentinel + `"}]`),
		Error:    json.RawMessage(`{"code": "extractor_failed", "message": "` + sentinel + `"}`),
	}, rg.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	checkKeys := func(where string, v any) {
		t.Helper()
		m, _ := v.(map[string]any)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, adminJobKeys) {
			t.Errorf("%s: keys %v, want exactly %v", where, keys, adminJobKeys)
		}
	}
	w := do(rg.admin, http.MethodGet, "/admin/v1/jobs", rg.token, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), sentinel) {
		t.Fatalf("GET /admin/v1/jobs = %d, content in the body: %t", w.Code, strings.Contains(w.Body.String(), sentinel))
	}
	var list struct{ Jobs []map[string]any }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Jobs) != 3 || list.Jobs[0]["id"] != ids[2] {
		t.Fatalf("list = %s (want 3, newest first)", w.Body)
	}
	for _, j := range list.Jobs {
		checkKeys("list", j)
	}
	failed := rg.do(t, http.MethodGet, "/admin/v1/jobs?status=failed&app="+testApp, "", http.StatusOK)
	if jobs, _ := failed["jobs"].([]any); len(jobs) != 1 || jobs[0].(map[string]any)["error_code"] != "extractor_failed" {
		t.Errorf("status filter = %v", failed)
	}
	if got := rg.do(t, http.MethodGet, "/admin/v1/jobs?app=nope", "", http.StatusOK); len(got["jobs"].([]any)) != 0 {
		t.Errorf("app filter = %v", got)
	}
	if got := rg.do(t, http.MethodGet, "/admin/v1/jobs?limit=1&since=1h", "", http.StatusOK); len(got["jobs"].([]any)) != 1 {
		t.Errorf("limit = %v", got)
	}
	for _, q := range []string{"status=done", "limit=0", "limit=1001", "since=yesterday"} {
		if w := do(rg.admin, http.MethodGet, "/admin/v1/jobs?"+q, rg.token, ""); w.Code != http.StatusBadRequest {
			t.Errorf("?%s = %d, want 400", q, w.Code)
		}
	}
	w = do(rg.admin, http.MethodGet, "/admin/v1/jobs/"+ids[0], rg.token, "")
	var one map[string]any
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), sentinel) || json.Unmarshal(w.Body.Bytes(), &one) != nil {
		t.Fatalf("GET job = %d %s", w.Code, w.Body)
	}
	checkKeys("get", one)

	// Force delete of a queued job: blobs, row, audit.
	rg.do(t, http.MethodDelete, "/admin/v1/jobs/"+ids[1], "", http.StatusNoContent)
	if _, err := rg.store.GetJobAny(ctx, ids[1]); err == nil {
		t.Error("the row survives the force delete")
	}
	if _, err := rg.blobs.Get(ctx, ids[1], blob.Document); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("the document blob survives the force delete: %v", err)
	}
	e := rg.lastAudit(audit.JobDeletedKind)
	if d, err := audit.Decode(e); err != nil || e.JobID != ids[1] || d.(audit.JobDeleted).Reason != audit.DeletedByAdmin {
		t.Errorf("job.deleted = %+v, %v", e, err)
	}
	if w := do(rg.admin, http.MethodDelete, "/admin/v1/jobs/"+ids[1], rg.token, ""); w.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", w.Code)
	}
}

func TestAdminAudit(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	rg.do(t, http.MethodPost, "/admin/v1/applications", `{"id": "dyn-a"}`, http.StatusCreated)
	got := rg.do(t, http.MethodGet, "/admin/v1/audit?kind=app.enabled&app=dyn-a", "", http.StatusOK)
	events, _ := got["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events = %v", got)
	}
	e := events[0].(map[string]any)
	if e["kind"] != "app.enabled" || e["actor"] != "admin:"+rg.tokenID || e["application_id"] != "dyn-a" {
		t.Errorf("event = %v", e)
	}
	rg.now = rg.now.Add(48 * time.Hour) // the default window is the last 24 hours
	if got := rg.do(t, http.MethodGet, "/admin/v1/audit?kind=app.enabled", "", http.StatusOK); len(got["events"].([]any)) != 0 {
		t.Errorf("default since: %v", got)
	}
	if got := rg.do(t, http.MethodGet, "/admin/v1/audit?kind=app.enabled&since=3d", "", http.StatusOK); len(got["events"].([]any)) == 0 {
		t.Errorf("since=3d: %v", got)
	}
	if w := do(rg.admin, http.MethodGet, "/admin/v1/audit?kind=admin.request", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d", w.Code)
	}
	if w := do(rg.admin, http.MethodGet, "/admin/v1/audit?kind=admin.request", rg.token, ""); w.Code != http.StatusBadRequest {
		t.Errorf("unknown kind = %d, want 400", w.Code)
	}
}

func TestAdminRateLimit(t *testing.T) {
	t.Parallel()
	rg := newAdminRig(t)
	for i := range adminRequestsPerMinute {
		if w := do(rg.admin, http.MethodGet, "/admin/v1/applications", rg.token, ""); w.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i+1, w.Code)
		}
	}
	w := do(rg.admin, http.MethodGet, "/admin/v1/applications", rg.token, "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Errorf("request %d = %d Retry-After %q, want 429 and 1", adminRequestsPerMinute+1, w.Code, w.Header().Get("Retry-After"))
	}
}

func countKind(rg *adminRig, kind audit.Kind) int {
	rg.store.mu.Lock()
	defer rg.store.mu.Unlock()
	n := 0
	for _, e := range rg.store.events {
		if e.Kind == string(kind) {
			n++
		}
	}
	return n
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func httpBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

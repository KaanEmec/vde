package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// The admin API (api/admin-openapi.yaml): run-time management of applications,
// tokens and jobs on its own listener, reached only with admin tokens. It calls the store
// methods the CLI calls (auth.Create, RevokeToken, ListTokens, ListAudit, …). Changes are
// audited with the fixed kinds (token.created, token.revoked, app.enabled, app.disabled,
// job.deleted) under the actor admin:<token id>; reads are only access-logged. Job
// listings carry metadata only, never result, feedback, instructions, schema or the
// attachment names.

// adminRequestsPerMinute is the admin API's rate limit per admin token.
const adminRequestsPerMinute = 60

// Listing limits: the default page and the largest a caller may ask for.
const (
	adminListDefault = 100
	adminListMax     = 1000
	adminAuditSince  = 24 * time.Hour // default window of GET /admin/v1/audit
	adminBodyBytes   = 64 << 10
	maxLabelChars    = 200 // auth.Create's limit
)

// appIDRe is the id of an application created over the admin API: it appears in URLs,
// metric labels and logs.
var appIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Messages of the admin API (errors.go explains the rules); %s is a field or parameter
// name the service defines.
const (
	msgAdminBadBody       = "The request body is not a JSON object with the documented fields."
	msgAdminInvalidField  = "The field %s is missing or invalid."
	msgAdminInvalidParam  = "The query parameter %s is invalid."
	msgAdminAppNotFound   = "No application with this id exists."
	msgAdminAppExists     = "An application with this id exists."
	msgAdminTokenNotFound = "No token with this id exists."
	msgAdminAppDisabled   = "The application is disabled."
	msgAdminJobNotFound   = "No job with this id exists."
	msgAdminRateLimited   = "The admin token's request rate limit is exceeded."
)

// adminRoutes is the admin listener's route table; the admin OpenAPI test checks it
// against api/admin-openapi.yaml. Every route needs the admin scope, then the per-token
// rate limit.
func (s *server) adminRoutes() []route {
	a := func(h http.HandlerFunc) http.Handler { return s.authenticate(auth.ScopeAdmin, s.adminLimit(h)) }
	return []route{
		{"GET /admin/v1/applications", a(s.adminListApps)},
		{"POST /admin/v1/applications", a(s.adminCreateApp)},
		{"GET /admin/v1/applications/{id}", a(s.adminGetApp)},
		{"PATCH /admin/v1/applications/{id}", a(s.adminPatchApp)},
		{"GET /admin/v1/applications/{id}/tokens", a(s.adminListTokens)},
		{"POST /admin/v1/applications/{id}/tokens", a(s.adminCreateToken)},
		{"GET /admin/v1/applications/{id}/quota", a(s.adminQuota)},
		{"DELETE /admin/v1/tokens/{id}", a(s.adminRevokeToken)},
		{"GET /admin/v1/jobs", a(s.adminListJobs)},
		{"GET /admin/v1/jobs/{id}", a(s.adminGetJob)},
		{"DELETE /admin/v1/jobs/{id}", a(s.adminDeleteJob)},
		{"GET /admin/v1/audit", a(s.adminListAudit)},
	}
}

// adminLimit is the admin rate limit: adminRequestsPerMinute per admin token.
func (s *server) adminLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := r.Context().Value(principalKey{}).(auth.Principal)
		if wait, ok := s.limits.take(p.Token.ID, bucketAdmin, adminRequestsPerMinute, s.now()); !ok {
			closeIfBodyPending(w, r)
			writeError(w, newError(codeRateLimited, msgAdminRateLimited).withRetryAfter(wait))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- applications

// appSettings are the settable fields of an application, in the shape of an
// applications[] entry of the YAML.
type appSettings struct {
	Name        string `json:"name"`
	MaxAttempts int    `json:"max_attempts"`
	Quota       struct {
		Limit  int    `json:"limit"`
		Window string `json:"window"`
	} `json:"quota"`
	Rate struct {
		RequestsPerMinute int `json:"requests_per_minute"`
		SubmitsPerMinute  int `json:"submits_per_minute"`
		MaxInProgress     int `json:"max_in_progress"`
	} `json:"rate"`
	Retention struct {
		Default   string `json:"default"`
		ForceNone bool   `json:"force_none"`
	} `json:"retention"`
	Documents struct {
		HTML string `json:"html"`
	} `json:"documents"`
	Judge appJudge `json:"judge"`
}

// appJudge is applications[].judge: policy "" means roles.judge.policy, sample_rate
// null means the default.
type appJudge struct {
	Policy        string   `json:"policy"`
	AllowOverride bool     `json:"allow_override"`
	SampleRate    *float64 `json:"sample_rate"`
}

// appBody is the body of POST and PATCH /admin/v1/applications[/{id}]. PATCH decodes
// it over the stored values, so absent fields keep them.
type appBody struct {
	ID      string `json:"id"`
	Enabled *bool  `json:"enabled"`
	appSettings
}

type appResponse struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"` // config (YAML wins at restart) or admin
	appSettings
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func settingsOf(a job.Application) appSettings {
	var st appSettings
	st.Name, st.MaxAttempts = a.Name, a.MaxAttempts
	st.Quota.Limit, st.Quota.Window = a.QuotaLimit, a.QuotaWindow
	st.Rate.RequestsPerMinute, st.Rate.SubmitsPerMinute, st.Rate.MaxInProgress =
		a.RequestsPerMinute, a.SubmitsPerMinute, a.MaxInProgress
	st.Retention.Default, st.Retention.ForceNone = a.RetentionDefault, a.ForceNone
	st.Documents.HTML = a.HTMLPolicy
	st.Judge = appJudge{Policy: a.JudgePolicy, AllowOverride: a.JudgeAllowOverride, SampleRate: a.JudgeSampleRate}
	return st
}

func appToResponse(a job.Application) appResponse {
	source := a.Source
	if source == "" {
		source = job.SourceConfig
	}
	return appResponse{
		ID: a.ID, Enabled: a.Enabled, Source: source, appSettings: settingsOf(a),
		CreatedAt: formatTime(a.CreatedAt), UpdatedAt: formatTime(a.UpdatedAt),
	}
}

// checkApp applies the YAML defaults to st and validates it like an applications[]
// entry (config.CheckApplication), returning the row to store or the refusal.
func (s *server) checkApp(id string, st appSettings) (job.Application, error) {
	c := config.Application{ID: id, Name: st.Name, MaxAttempts: st.MaxAttempts}
	c.Quota.Limit, c.Quota.Window = st.Quota.Limit, st.Quota.Window
	c.Rate.RequestsPerMinute, c.Rate.SubmitsPerMinute, c.Rate.MaxInProgress =
		st.Rate.RequestsPerMinute, st.Rate.SubmitsPerMinute, st.Rate.MaxInProgress
	c.Retention.Default, c.Retention.ForceNone = st.Retention.Default, st.Retention.ForceNone
	c.Documents.HTML = st.Documents.HTML
	c.Judge = config.AppJudge{
		Policy: config.JudgePolicy(st.Judge.Policy), AllowOverride: st.Judge.AllowOverride, SampleRate: st.Judge.SampleRate,
	}
	for _, issue := range s.deps.Config.CheckApplication(&c) {
		if !issue.Warning {
			return job.Application{}, newError(codeBadRequest, msgAdminInvalidField, issue.Key)
		}
	}
	return job.Application{
		ID: id, Name: c.Name, MaxAttempts: c.MaxAttempts, QuotaLimit: c.Quota.Limit,
		QuotaWindow: c.Quota.Window, RequestsPerMinute: c.Rate.RequestsPerMinute,
		SubmitsPerMinute: c.Rate.SubmitsPerMinute, MaxInProgress: c.Rate.MaxInProgress,
		RetentionDefault: c.Retention.Default, ForceNone: c.Retention.ForceNone,
		HTMLPolicy: c.Documents.HTML, JudgePolicy: string(c.Judge.Policy),
		JudgeAllowOverride: c.Judge.AllowOverride, JudgeSampleRate: c.Judge.SampleRate,
	}, nil
}

func (s *server) adminListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.deps.Store.ListApplications(r.Context())
	if err != nil {
		s.jobError(w, r, job.Job{}, "applications not listed", err)
		return
	}
	out := make([]appResponse, len(apps))
	for i, a := range apps {
		out[i] = appToResponse(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out})
}

// adminCreateApp is POST /admin/v1/applications: a dynamic application (source admin),
// enabled unless the body says otherwise. 409 when the id exists (YAML or admin).
func (s *server) adminCreateApp(w http.ResponseWriter, r *http.Request) {
	var body appBody
	if !decodeAdminBody(w, r, &body) {
		return
	}
	if !appIDRe.MatchString(body.ID) {
		writeError(w, newError(codeBadRequest, msgAdminInvalidField, "id"))
		return
	}
	row, err := s.checkApp(body.ID, body.appSettings)
	if err != nil {
		writeError(w, err)
		return
	}
	row.Enabled = body.Enabled == nil || *body.Enabled
	ctx := r.Context()
	switch err := s.deps.Store.CreateApplication(ctx, row, s.now()); {
	case errors.Is(err, job.ErrDuplicate):
		writeError(w, newError(codeConflict, msgAdminAppExists))
		return
	case err != nil:
		s.jobError(w, r, job.Job{ApplicationID: row.ID}, "application not created", err)
		return
	}
	if row.Enabled {
		s.recordAudit(ctx, row.ID, "", audit.AppEnabled{})
	}
	s.writeApp(w, r, row.ID, http.StatusCreated)
}

func (s *server) adminGetApp(w http.ResponseWriter, r *http.Request) {
	s.writeApp(w, r, r.PathValue("id"), http.StatusOK)
}

// adminPatchApp is PATCH /admin/v1/applications/{id}: the fields present replace the
// stored ones (a zero value means the YAML default). For a source config application
// the next start rewrites every field from the YAML again.
func (s *server) adminPatchApp(w http.ResponseWriter, r *http.Request) {
	prev, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	body := appBody{appSettings: settingsOf(prev)}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	if body.ID != "" && body.ID != prev.ID {
		writeError(w, newError(codeBadRequest, msgAdminInvalidField, "id"))
		return
	}
	row, err := s.checkApp(prev.ID, body.appSettings)
	if err != nil {
		writeError(w, err)
		return
	}
	row.Enabled = prev.Enabled
	if body.Enabled != nil {
		row.Enabled = *body.Enabled
	}
	ctx := r.Context()
	if err := s.deps.Store.UpdateApplication(ctx, row, s.now()); err != nil {
		s.jobError(w, r, job.Job{ApplicationID: row.ID}, "application not updated", err)
		return
	}
	switch {
	case row.Enabled && !prev.Enabled:
		s.recordAudit(ctx, row.ID, "", audit.AppEnabled{})
	case !row.Enabled && prev.Enabled:
		s.recordAudit(ctx, row.ID, "", audit.AppDisabled{})
	}
	s.writeApp(w, r, row.ID, http.StatusOK)
}

func (s *server) writeApp(w http.ResponseWriter, r *http.Request, id string, status int) {
	r.SetPathValue("id", id)
	a, ok := s.loadApp(w, r)
	if ok {
		writeJSON(w, status, appToResponse(a))
	}
}

// loadApp reads the application of the path's {id}; it reports false after answering.
func (s *server) loadApp(w http.ResponseWriter, r *http.Request) (job.Application, bool) {
	a, err := s.deps.Store.GetApplication(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, job.ErrNotFound):
		writeError(w, newError(codeNotFound, msgAdminAppNotFound))
		return a, false
	case err != nil:
		s.jobError(w, r, job.Job{}, "application not readable", err)
		return a, false
	}
	return a, true
}

// adminQuota is GET /admin/v1/applications/{id}/quota: GET /v1/quota for any
// application, plus the count used.
func (s *server) adminQuota(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	q, err := s.currentQuota(r.Context(), a)
	if err != nil {
		s.jobError(w, r, job.Job{ApplicationID: a.ID}, "quota not readable", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limit": q.Limit, "used": q.Limit - q.Remaining, "remaining": q.Remaining,
		"reset": formatTime(q.Reset), "window": q.Window,
	})
}

// ---- tokens

// tokenResponse is a token's metadata: never the secret or its hash.
type tokenResponse struct {
	ID            string   `json:"id"`
	ApplicationID string   `json:"application_id"`
	Scopes        []string `json:"scopes"`
	Label         string   `json:"label"`
	Status        string   `json:"status"` // active, revoked or expired
	ExpiresAt     string   `json:"expires_at"`
	RevokedAt     *string  `json:"revoked_at"`
	CreatedAt     string   `json:"created_at"`
	LastUsedAt    *string  `json:"last_used_at"`
}

func (s *server) tokenToResponse(t job.Token) tokenResponse {
	status := "active"
	switch {
	case !t.RevokedAt.IsZero():
		status = "revoked"
	case !s.now().Before(t.ExpiresAt):
		status = "expired"
	}
	return tokenResponse{
		ID: t.ID, ApplicationID: t.ApplicationID, Scopes: t.Scopes, Label: t.Label, Status: status,
		ExpiresAt: formatTime(t.ExpiresAt), RevokedAt: optionalTime(t.RevokedAt),
		CreatedAt: formatTime(t.CreatedAt), LastUsedAt: optionalTime(t.LastUsedAt),
	}
}

func (s *server) adminListTokens(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	tokens, err := s.deps.Store.ListTokens(r.Context(), a.ID)
	if err != nil {
		s.jobError(w, r, job.Job{ApplicationID: a.ID}, "tokens not listed", err)
		return
	}
	out := make([]tokenResponse, len(tokens))
	for i, t := range tokens {
		out[i] = s.tokenToResponse(t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// adminCreateToken is POST /admin/v1/applications/{id}/tokens, vde token create over
// HTTP (auth.Create): the token is in the 201 body once. Admin tokens are created by the
// CLI only, so the admin scope is refused here.
func (s *server) adminCreateToken(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var body struct {
		Scopes  []string `json:"scopes"`
		Expires string   `json:"expires"` // as vde token create --expires
		Label   string   `json:"label"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	scopes, err := auth.ParseScopes(strings.Join(body.Scopes, ","))
	if err != nil || len(body.Scopes) == 0 {
		writeError(w, newError(codeBadRequest, msgAdminInvalidField, "scopes"))
		return
	}
	now := s.now()
	expires, err := auth.ParseExpires(body.Expires, now)
	if err != nil {
		writeError(w, newError(codeBadRequest, msgAdminInvalidField, "expires"))
		return
	}
	maxTTL := s.deps.Config.Auth.MaxTokenTTL
	// auth.Create checks the same; checked here first to name the field.
	switch {
	case !expires.After(now) || expires.Sub(now) > maxTTL:
		writeError(w, newError(codeBadRequest, msgAdminInvalidField, "expires"))
		return
	case len(body.Label) > maxLabelChars:
		writeError(w, newError(codeBadRequest, msgAdminInvalidField, "label"))
		return
	case !a.Enabled:
		writeError(w, newError(codeConflict, msgAdminAppDisabled))
		return
	}
	ctx := r.Context()
	token, row, err := auth.Create(ctx, s.deps.Store, auth.Request{
		ApplicationID: a.ID, Scopes: scopes, ExpiresAt: expires, Label: body.Label,
	}, maxTTL, now)
	if err != nil {
		s.jobError(w, r, job.Job{ApplicationID: a.ID}, "token not created", err)
		return
	}
	s.recordAudit(ctx, a.ID, "", audit.TokenCreated{TokenID: row.ID, Scopes: row.Scopes, ExpiresAt: row.ExpiresAt.UTC()})
	writeJSON(w, http.StatusCreated, struct {
		Token string `json:"token"`
		tokenResponse
	}{token, s.tokenToResponse(row)})
}

// adminRevokeToken is DELETE /admin/v1/tokens/{id}, vde token revoke over HTTP, for
// application and admin tokens alike; revoking a revoked token is 204 again.
func (s *server) adminRevokeToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := s.deps.Store.GetTokenByID(ctx, r.PathValue("id"))
	switch {
	case errors.Is(err, job.ErrNotFound):
		writeError(w, newError(codeNotFound, msgAdminTokenNotFound))
		return
	case err != nil:
		s.jobError(w, r, job.Job{}, "token not readable", err)
		return
	}
	if t.RevokedAt.IsZero() {
		if err := s.deps.Store.RevokeToken(ctx, t.ID, s.now()); err != nil {
			s.jobError(w, r, job.Job{ApplicationID: t.ApplicationID}, "token not revoked", err)
			return
		}
		s.recordAudit(ctx, t.ApplicationID, "", audit.TokenRevoked{TokenID: t.ID})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- jobs

// adminJobResponse is a job's metadata. It never carries the result, feedback, error
// message, instructions, schema, idempotency key or attachment names.
type adminJobResponse struct {
	ID               string     `json:"id"`
	ApplicationID    string     `json:"application_id"`
	Status           job.Status `json:"status"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"` // the request's limit; 0 means the application's
	ServiceVersion   string     `json:"service_version"`
	JudgePolicy      string     `json:"judge_policy"`
	Verification     *string    `json:"verification"` // null until terminal
	Representation   string     `json:"representation"`
	MediaType        string     `json:"media_type"`
	Bytes            int64      `json:"bytes"`
	Pages            int        `json:"pages"`
	Retention        string     `json:"retention"`
	ErrorCode        string     `json:"error_code"` // "" unless failed
	CreatedAt        string     `json:"created_at"`
	StartedAt        *string    `json:"started_at"`
	FinishedAt       *string    `json:"finished_at"`
	ExpiresAt        string     `json:"expires_at"`
	InputsDeletedAt  *string    `json:"inputs_deleted_at"`
	ContentDeletedAt *string    `json:"content_deleted_at"`
	InputTokens      int64      `json:"input_tokens"`  // provider token usage, all runs
	OutputTokens     int64      `json:"output_tokens"` // provider token usage, all runs
}

func adminJob(j job.Job) adminJobResponse {
	var e struct {
		Code string `json:"code"`
	}
	if j.Error != nil {
		_ = json.Unmarshal(j.Error, &e) // the message is never copied
	}
	var verification *string
	if j.Status.Terminal() {
		v := cmp.Or(j.Verification, job.DefaultVerification(j.Status))
		verification = &v
	}
	return adminJobResponse{
		ID: j.ID, ApplicationID: j.ApplicationID, Status: j.Status, Attempts: j.Attempts,
		MaxAttempts: j.MaxAttempts, ServiceVersion: j.ServiceVersion,
		JudgePolicy: cmp.Or(j.JudgePolicy, job.DefaultJudgePolicy), Verification: verification,
		Representation: j.Representation,
		MediaType:      j.MediaType, Bytes: j.Size, Pages: j.PageCount, Retention: string(j.Retention),
		ErrorCode: e.Code, CreatedAt: formatTime(j.CreatedAt), StartedAt: optionalTime(j.StartedAt),
		FinishedAt: optionalTime(j.FinishedAt), ExpiresAt: formatTime(j.ExpiresAt),
		InputsDeletedAt: optionalTime(j.InputsDeletedAt), ContentDeletedAt: optionalTime(j.ContentDeletedAt),
		InputTokens: j.InputTokens, OutputTokens: j.OutputTokens,
	}
}

// adminListJobs is GET /admin/v1/jobs?app=&status=&since=&limit=, newest first.
func (s *server) adminListJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := job.JobFilter{ApplicationID: q.Get("app"), Status: job.Status(q.Get("status"))}
	if f.Status != "" && !slices.Contains(job.Statuses, f.Status) {
		writeError(w, newError(codeBadRequest, msgAdminInvalidParam, "status"))
		return
	}
	since, limit, ok := s.listParams(w, r, time.Time{})
	if !ok {
		return
	}
	f.Since = since
	jobs, err := s.deps.Store.ListJobs(r.Context(), f, limit)
	if err != nil {
		s.jobError(w, r, job.Job{}, "jobs not listed", err)
		return
	}
	out := make([]adminJobResponse, len(jobs))
	for i, j := range jobs {
		out[i] = adminJob(j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *server) adminGetJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.loadAnyJob(w, r)
	if ok {
		writeJSON(w, http.StatusOK, adminJob(j))
	}
}

// adminDeleteJob is DELETE /admin/v1/jobs/{id}: blobs, then the row with its content,
// in any state. A worker still running the job loses it at its next write. 204; only
// the audit record (job.deleted, reason admin) stays.
func (s *server) adminDeleteJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.loadAnyJob(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.deps.Blobs.DeleteAll(ctx, j.ID); err != nil {
		s.jobError(w, r, j, "job blobs not deleted", err)
		return
	}
	switch err := s.deps.Store.DeleteJob(ctx, j.ID); {
	case errors.Is(err, job.ErrNotFound):
		writeError(w, newError(codeNotFound, msgAdminJobNotFound))
		return
	case err != nil:
		s.jobError(w, r, j, "job not deleted", err)
		return
	}
	s.recordAudit(r.Context(), j.ApplicationID, j.ID, audit.JobDeleted{Reason: audit.DeletedByAdmin, Retention: string(j.Retention)})
	s.refreshInProgress(ctx, j.ApplicationID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) loadAnyJob(w http.ResponseWriter, r *http.Request) (job.Job, bool) {
	j, err := s.deps.Store.GetJobAny(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, job.ErrNotFound):
		writeError(w, newError(codeNotFound, msgAdminJobNotFound))
		return j, false
	case err != nil:
		s.jobError(w, r, job.Job{}, "job not readable", err)
		return j, false
	}
	return j, true
}

// ---- audit

type auditResponse struct {
	ID            int64           `json:"id"`
	At            string          `json:"at"`
	Kind          string          `json:"kind"`
	ApplicationID string          `json:"application_id"`
	Actor         string          `json:"actor"`
	JobID         string          `json:"job_id"`
	Details       json.RawMessage `json:"details"`
}

// adminListAudit is GET /admin/v1/audit?app=&since=&kind=&limit=, oldest first, the
// last 24 hours unless since is given (vde audit list over HTTP).
func (s *server) adminListAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := job.AuditFilter{ApplicationID: q.Get("app"), Kind: q.Get("kind")}
	if f.Kind != "" && !slices.Contains(audit.Kinds(), audit.Kind(f.Kind)) {
		writeError(w, newError(codeBadRequest, msgAdminInvalidParam, "kind"))
		return
	}
	since, limit, ok := s.listParams(w, r, s.now().Add(-adminAuditSince))
	if !ok {
		return
	}
	f.Since = since
	events, err := s.deps.Store.ListAudit(r.Context(), f, limit)
	if err != nil {
		s.jobError(w, r, job.Job{}, "audit not listed", err)
		return
	}
	out := make([]auditResponse, len(events))
	for i, e := range events {
		details := e.Details
		if len(details) == 0 {
			details = json.RawMessage("{}")
		}
		out[i] = auditResponse{
			ID: e.ID, At: e.At.UTC().Format(time.RFC3339Nano), Kind: e.Kind, ApplicationID: e.ApplicationID,
			Actor: e.Actor, JobID: e.JobID, Details: details,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// ---- helpers

// listParams reads since (audit.ParseSince; defaultSince when absent) and limit (1 to
// adminListMax, adminListDefault when absent). It reports false after answering 400.
func (s *server) listParams(w http.ResponseWriter, r *http.Request, defaultSince time.Time) (time.Time, int, bool) {
	q := r.URL.Query()
	since := defaultSince
	if v := q.Get("since"); v != "" {
		t, err := audit.ParseSince(v, s.now())
		if err != nil {
			writeError(w, newError(codeBadRequest, msgAdminInvalidParam, "since"))
			return since, 0, false
		}
		since = t
	}
	limit := adminListDefault
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > adminListMax {
			writeError(w, newError(codeBadRequest, msgAdminInvalidParam, "limit"))
			return since, 0, false
		}
		limit = n
	}
	return since, limit, true
}

// decodeAdminBody decodes a JSON body of at most adminBodyBytes into v, refusing
// unknown fields and trailing data. It reports false after answering 400.
func decodeAdminBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		writeError(w, newError(codeBadRequest, msgAdminBadBody))
		return false
	}
	return true
}

// optionalTime is formatTime, nil (null) for a zero time.
func optionalTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	f := formatTime(t)
	return &f
}

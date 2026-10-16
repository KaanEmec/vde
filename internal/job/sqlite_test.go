package job

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func openTest(t *testing.T) *SQLite {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "nested", "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testApp(id string) Application {
	return Application{
		ID: id, Name: id, MaxAttempts: 3, QuotaLimit: 1000, QuotaWindow: "P1D",
		RequestsPerMinute: 300, SubmitsPerMinute: 30, MaxInProgress: 20, RetentionDefault: "48h",
	}
}

func seedApp(t *testing.T, s *SQLite, a Application) {
	t.Helper()
	if err := s.UpsertApplication(t.Context(), a, t0); err != nil {
		t.Fatal(err)
	}
}

func newJob(app string) *Job {
	return &Job{
		ApplicationID: app, ServiceVersion: "vde-0.2.0+c0ffee12", Representation: "text",
		MediaType: "text/plain; charset=utf-8", Size: 5, Retention: RetentionDefault,
		ExtractorInstructions: "extract", JudgeInstructions: "judge", Schema: `{"type":"object"}`,
		CreatedAt: t0,
	}
}

func createJob(t *testing.T, s *SQLite, j *Job) *Job {
	t.Helper()
	if err := s.CreateJob(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestOpenPermissionsAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "vde.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("database file mode = %o, want 600", perm)
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("database directory mode = %o, want 700", perm)
	}
	for pragma, want := range map[string]string{
		"journal_mode": "wal", "busy_timeout": "5000", "foreign_keys": "1", "synchronous": "1",
	} {
		var got string
		if err := s.db.QueryRowContext(t.Context(), "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
	// Reopening applies nothing new.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
}

func TestApplications(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	a := testApp("travelinbox-dev")
	a.ForceNone, a.HTMLPolicy = true, "visible_text"
	seedApp(t, s, a)
	seedApp(t, s, testApp("other"))

	got, err := s.GetApplication(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.ForceNone || got.HTMLPolicy != "visible_text" || !got.CreatedAt.Equal(t0) {
		t.Errorf("GetApplication = %+v", got)
	}

	a.Name = "renamed"
	if err := s.UpsertApplication(ctx, a, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetApplication(ctx, a.ID)
	if got.Name != "renamed" || !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("after upsert = %+v", got)
	}

	disabled, err := s.DisableApplicationsNotIn(ctx, []string{a.ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(disabled, []string{"other"}) {
		t.Errorf("disabled = %v, want [other]", disabled)
	}
	if disabled, _ := s.DisableApplicationsNotIn(ctx, []string{a.ID}, t0); len(disabled) != 0 {
		t.Errorf("second disable = %v, want none", disabled)
	}
	list, err := s.ListApplications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "other" || list[0].Enabled || !list[1].Enabled {
		t.Errorf("ListApplications = %+v", list)
	}
	seedApp(t, s, testApp("other")) // back in the configuration: enabled again
	if got, _ := s.GetApplication(ctx, "other"); !got.Enabled {
		t.Error("upsert did not re-enable")
	}
	if _, err := s.GetApplication(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing application: %v, want ErrNotFound", err)
	}
}

func TestTokens(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedApp(t, s, testApp("app"))
	tok := Token{
		ID: "abcd1234", ApplicationID: "app", Hash: bytes.Repeat([]byte{7}, 32),
		Scopes: []string{"jobs", "jobs:read"}, Label: "ci", ExpiresAt: t0.AddDate(1, 0, 0), CreatedAt: t0,
	}
	if err := s.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, tok); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate token: %v, want ErrDuplicate", err)
	}
	bad := tok
	bad.ID, bad.ApplicationID = "other", "missing"
	if err := s.CreateToken(ctx, bad); err == nil {
		t.Error("token for an unknown application was stored")
	}

	got, err := s.GetTokenByID(ctx, tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Hash, tok.Hash) || !slices.Equal(got.Scopes, tok.Scopes) || !got.ExpiresAt.Equal(tok.ExpiresAt) ||
		!got.RevokedAt.IsZero() || !got.LastUsedAt.IsZero() {
		t.Errorf("GetTokenByID = %+v", got)
	}

	if err := s.TouchTokenLastUsed(ctx, tok.ID, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchTokenLastUsed(ctx, tok.ID, t0.Add(90*time.Second)); err != nil { // within a minute: no write
		t.Fatal(err)
	}
	got, _ = s.GetTokenByID(ctx, tok.ID)
	if !got.LastUsedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("last_used_at = %v, want %v", got.LastUsedAt, t0.Add(time.Minute))
	}
	if err := s.TouchTokenLastUsed(ctx, tok.ID, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTokenByID(ctx, tok.ID)
	if !got.LastUsedAt.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("last_used_at = %v, want %v", got.LastUsedAt, t0.Add(2*time.Minute))
	}

	if err := s.RevokeToken(ctx, tok.ID, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken(ctx, tok.ID, t0.Add(2*time.Hour)); err != nil { // keeps the first time
		t.Fatal(err)
	}
	got, _ = s.GetTokenByID(ctx, tok.ID)
	if !got.RevokedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("revoked_at = %v", got.RevokedAt)
	}
	if err := s.RevokeToken(ctx, "missing", t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke missing: %v", err)
	}
	if _, err := s.GetTokenByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}

	second := tok
	second.ID, second.CreatedAt = "efgh5678", t0.Add(time.Second)
	if err := s.CreateToken(ctx, second); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListTokens(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != tok.ID || list[1].ID != second.ID {
		t.Errorf("ListTokens = %+v", list)
	}
}

func TestQuota(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedApp(t, s, testApp("app"))
	window := t0.Truncate(24 * time.Hour)
	if n, err := s.GetQuota(ctx, "app", window); err != nil || n != 0 {
		t.Fatalf("GetQuota before = %d, %v", n, err)
	}
	for want := 1; want <= 3; want++ {
		n, err := s.IncrementQuota(ctx, "app", window, 3)
		if err != nil || n != want {
			t.Fatalf("IncrementQuota = %d, %v; want %d", n, err, want)
		}
	}
	if _, err := s.IncrementQuota(ctx, "app", window, 3); !errors.Is(err, ErrQuotaExhausted) {
		t.Errorf("over limit: %v, want ErrQuotaExhausted", err)
	}
	if _, err := s.IncrementQuota(ctx, "app", window.Add(24*time.Hour), 0); !errors.Is(err, ErrQuotaExhausted) {
		t.Errorf("limit 0: %v, want ErrQuotaExhausted", err)
	}
	if n, _ := s.GetQuota(ctx, "app", window); n != 3 {
		t.Errorf("GetQuota = %d, want 3", n)
	}
	if n, _ := s.IncrementQuota(ctx, "app", window.Add(24*time.Hour), 3); n != 1 {
		t.Errorf("next window = %d, want 1", n)
	}
}

func TestAppendAudit(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	id1, err := s.AppendAudit(ctx, AuditEvent{At: t0, Kind: "token.created", ApplicationID: "app", Actor: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.AppendAudit(ctx, AuditEvent{At: t0, Kind: "job.finished", JobID: "j_1", Details: json.RawMessage(`{"status":"accepted"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if id2 <= id1 {
		t.Errorf("ids %d then %d, want increasing", id1, id2)
	}
	if _, err := s.AppendAudit(ctx, AuditEvent{At: t0, Kind: "x", Details: json.RawMessage(`not json`)}); err == nil {
		t.Error("invalid details accepted")
	}
	if _, err := s.AppendAudit(ctx, AuditEvent{Kind: "x"}); err == nil {
		t.Error("event without time accepted")
	}
	var details string
	if err := s.db.QueryRowContext(ctx, `SELECT details FROM audit_events WHERE id = ?`, id1).Scan(&details); err != nil || details != "{}" {
		t.Errorf("default details = %q, %v", details, err)
	}
}

func TestCreateAndGetJob(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedApp(t, s, testApp("app"))
	seedApp(t, s, testApp("other"))

	j := newJob("app")
	j.CreatedAt = t0.Add(123456789 * time.Nanosecond) // truncated to milliseconds
	j.IdempotencyKey, j.BodyHash, j.PageCount, j.MaxAttempts = "key-1", "sha256:abc", 2, 2
	createJob(t, s, j)
	if len(j.ID) != 28 || j.ID[:2] != "j_" {
		t.Errorf("id = %q, want j_<ULID>", j.ID)
	}
	if j.Status != StatusQueued || !j.CreatedAt.Equal(t0.Add(123*time.Millisecond)) {
		t.Errorf("created = %+v", j)
	}
	if want := j.CreatedAt.Add(48 * time.Hour); !j.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v (application retention)", j.ExpiresAt, want)
	}

	got, err := s.GetJob(ctx, "app", j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusQueued || got.Result != nil || got.Feedback != nil || got.Error != nil ||
		got.IdempotencyKey != "key-1" || got.PageCount != 2 || got.MaxAttempts != 2 || got.Schema != j.Schema ||
		!got.CreatedAt.Equal(j.CreatedAt) || !got.StartedAt.IsZero() || got.JudgeConfidence != nil {
		t.Errorf("GetJob = %+v", got)
	}
	if _, err := s.GetJob(ctx, "other", j.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign GetJob: %v, want ErrNotFound", err)
	}
	if _, err := s.GetJobAny(ctx, j.ID); err != nil {
		t.Errorf("GetJobAny: %v", err)
	}

	found, err := s.FindByIdempotencyKey(ctx, "app", "key-1")
	if err != nil || found.ID != j.ID {
		t.Errorf("FindByIdempotencyKey = %v, %v", found.ID, err)
	}
	if _, err := s.FindByIdempotencyKey(ctx, "other", "key-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign key lookup: %v", err)
	}
	dup := newJob("app")
	dup.IdempotencyKey = "key-1"
	if err := s.CreateJob(ctx, dup); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate key: %v, want ErrDuplicate", err)
	}
	sameKeyOtherApp := newJob("other")
	sameKeyOtherApp.IdempotencyKey = "key-1"
	createJob(t, s, sameKeyOtherApp)
	createJob(t, s, newJob("app")) // no key: many allowed
	createJob(t, s, newJob("app"))

	if err := s.CreateJob(ctx, newJob("missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown application: %v", err)
	}
	if _, err := s.DisableApplicationsNotIn(ctx, []string{"app"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateJob(ctx, newJob("other")); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled application: %v", err)
	}
	if err := s.CreateJob(ctx, &Job{ApplicationID: "app"}); err == nil {
		t.Error("job without required fields stored")
	}
}

func TestCreateJobRetention(t *testing.T) {
	s := openTest(t)
	forced := testApp("forced")
	forced.ForceNone = true
	noneDefault := testApp("none-default")
	noneDefault.RetentionDefault = "none"
	for _, a := range []Application{testApp("app"), forced, noneDefault} {
		seedApp(t, s, a)
	}
	tests := []struct {
		app       string
		requested Retention
		want      Retention
		expires   time.Duration
	}{
		{"app", RetentionNone, RetentionNone, 24 * time.Hour},
		{"app", RetentionDefault, RetentionDefault, 48 * time.Hour},
		{"app", "", RetentionDefault, 48 * time.Hour},
		{"forced", RetentionDefault, RetentionNone, 24 * time.Hour},
		{"none-default", RetentionDefault, RetentionNone, 24 * time.Hour},
	}
	for _, tt := range tests {
		j := newJob(tt.app)
		j.Retention = tt.requested
		createJob(t, s, j)
		if j.Retention != tt.want || !j.ExpiresAt.Equal(t0.Add(tt.expires)) {
			t.Errorf("%s/%q: retention %q expires %v, want %q %v", tt.app, tt.requested, j.Retention, j.ExpiresAt, tt.want, t0.Add(tt.expires))
		}
	}
}

func TestMarkContentDeletedAndDelete(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedApp(t, s, testApp("app"))
	j := createJob(t, s, newJob("app"))

	if err := s.MarkContentDeleted(ctx, j.ID, ContentAll, t0); !errors.Is(err, ErrConflict) {
		t.Errorf("ContentAll on a queued job: %v, want ErrConflict", err)
	}
	finishJob(t, s, j.ID, StatusAccepted)
	if err := s.MarkContentDeleted(ctx, j.ID, ContentInputs, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJobAny(ctx, j.ID)
	if got.ExtractorInstructions != "" || got.JudgeInstructions != "" || got.Schema != "" ||
		!got.InputsDeletedAt.Equal(t0.Add(time.Minute)) || got.Result == nil || !got.ContentDeletedAt.IsZero() {
		t.Errorf("after ContentInputs = %+v", got)
	}
	if err := s.MarkContentDeleted(ctx, j.ID, ContentAll, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJobAny(ctx, j.ID)
	if got.Result != nil || got.Feedback != nil || got.Error != nil || got.JudgeConfidence != nil ||
		!got.ContentDeletedAt.Equal(t0.Add(2*time.Minute)) || !got.InputsDeletedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("after ContentAll = %+v", got)
	}
	if err := s.MarkContentDeleted(ctx, j.ID, ContentAll, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("second ContentAll: %v, want ErrNotFound", err)
	}
	if err := s.MarkContentDeleted(ctx, "j_missing", ContentInputs, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing job: %v", err)
	}
	if err := s.DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob(ctx, j.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if _, err := s.GetJobAny(ctx, j.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
}

func TestListExpiredAndCountRunning(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedApp(t, s, testApp("app"))
	var ids []string
	for i := range 3 {
		j := newJob("app")
		j.Retention = RetentionNone
		j.CreatedAt = t0.Add(time.Duration(i) * time.Hour)
		ids = append(ids, createJob(t, s, j).ID)
	}
	expired, err := s.ListExpired(ctx, t0.Add(25*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 2 || expired[0].ID != ids[0] || expired[1].ID != ids[1] {
		t.Errorf("ListExpired = %d jobs", len(expired))
	}
	if expired, _ := s.ListExpired(ctx, t0.Add(48*time.Hour), 1); len(expired) != 1 {
		t.Errorf("limit 1 = %d jobs", len(expired))
	}

	if n, _ := s.CountRunning(ctx, "app"); n != 0 {
		t.Errorf("CountRunning = %d, want 0", n)
	}
	if _, err := s.LeaseNextQueued(ctx, "w1", t0, t0.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountRunning(ctx, "app"); n != 1 {
		t.Errorf("CountRunning = %d, want 1", n)
	}
}

func finishJob(t *testing.T, s *SQLite, id string, status Status) {
	t.Helper()
	ctx := t.Context()
	leased, err := s.LeaseNextQueued(ctx, "finisher", t0, t0.Add(time.Minute), nil)
	if err != nil || leased.ID != id {
		t.Fatalf("lease = %q, %v; want %q", leased.ID, err, id)
	}
	confidence := 0.9
	err = s.Finish(ctx, id, "finisher", Outcome{
		Status: status, Attempts: 1, Result: json.RawMessage(`{"a":1}`), Feedback: json.RawMessage(`[]`),
		JudgeConfidence: &confidence,
	}, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLeaseMaxInProgress(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedApp(t, s, testApp("busy"))
	seedApp(t, s, testApp("calm"))
	for i := range 3 {
		j := newJob("busy")
		j.CreatedAt = t0.Add(time.Duration(i) * time.Millisecond)
		createJob(t, s, j)
	}
	late := newJob("calm")
	late.CreatedAt = t0.Add(time.Second)
	createJob(t, s, late)

	limits := map[string]int{"busy": 2, "calm": 0}
	lease := func() string {
		t.Helper()
		j, err := s.LeaseNextQueued(ctx, "w", t0.Add(2*time.Second), t0.Add(time.Minute), limits)
		if errors.Is(err, ErrNotFound) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return j.ApplicationID
	}
	got := []string{lease(), lease(), lease(), lease()}
	if want := []string{"busy", "busy", "calm", ""}; !slices.Equal(got, want) {
		t.Errorf("lease order = %q, want %q (busy capped at 2)", got, want)
	}
}

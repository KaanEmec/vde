package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/job"
)

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func openStore(t *testing.T) *job.DB {
	t.Helper()
	s, err := job.Open(filepath.Join(t.TempDir(), "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// samples has one filled value per kind.
var samples = map[audit.Kind]audit.Details{
	audit.TokenCreatedKind: audit.TokenCreated{TokenID: "tok_abc", Scopes: []string{"jobs:write", "jobs:read"}, ExpiresAt: t0.Add(24 * time.Hour)},
	audit.TokenRevokedKind: audit.TokenRevoked{TokenID: "tok_abc"},
	audit.AppEnabledKind:   audit.AppEnabled{},
	audit.AppDisabledKind:  audit.AppDisabled{},
	audit.JobCreatedKind: audit.JobCreated{
		CreatedAt: t0, MediaType: "application/pdf", Size: 1234, PageCount: 3, Retention: "none", ServiceVersion: "vde-0.2.0+c0ffee12",
	},
	audit.JobFinishedKind: audit.JobFinished{Status: "failed", Attempts: 2, ErrorCode: "timeout", ServiceVersion: "vde-0.2.0+c0ffee12", Verification: "grounded", JudgePolicy: "auto"},
	audit.JobDeletedKind:  audit.JobDeleted{Reason: audit.DeletedFirstRead, Retention: "none"},
	audit.JobExpiredKind:  audit.JobExpired{Status: "accepted", Retention: "default"},
	audit.AuthFailedKind:  audit.AuthFailed{Reason: "expired"},
	audit.ConfigLoadedKind: audit.ConfigLoaded{
		ServiceVersion: "vde-0.2.0+c0ffee12",
	},
	audit.WebhookDeliveredKind: audit.WebhookDelivered{Event: "job.finished", Attempt: 2, StatusCode: 204},
	audit.WebhookFailedKind:    audit.WebhookFailed{Event: "job.finished", Attempt: 1, Reason: audit.WebhookStatus, StatusCode: 503},
}

func TestKindsAreFixed(t *testing.T) {
	want := []string{
		"token.created", "token.revoked", "app.enabled", "app.disabled", "job.created",
		"job.finished", "job.deleted", "job.expired", "auth.failed", "config.loaded",
		"webhook.delivered", "webhook.failed",
	}
	var got []string
	for _, k := range audit.Kinds() {
		got = append(got, string(k))
		if d, ok := samples[k]; !ok || d.AuditKind() != k {
			t.Errorf("kind %s has no matching details struct", k)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Kinds() = %v, want %v", got, want)
	}
	if len(samples) != len(want) {
		t.Errorf("%d samples for %d kinds", len(samples), len(want))
	}
}

// No free-form data: details structs hold scalars, times and string slices only.
func TestDetailsHaveNoFreeFormFields(t *testing.T) {
	for k, d := range samples {
		typ := reflect.TypeOf(d)
		for f := range typ.Fields() {
			ft := f.Type
			if ft.Kind() == reflect.Slice {
				ft = ft.Elem()
			}
			switch ft.Kind() {
			case reflect.String, reflect.Int, reflect.Int64, reflect.Bool:
			case reflect.Struct:
				if ft != reflect.TypeFor[time.Time]() {
					t.Errorf("%s.%s: struct field %s", k, f.Name, ft)
				}
			default:
				t.Errorf("%s.%s: field of kind %s", k, f.Name, ft.Kind())
			}
			if f.Tag.Get("json") == "" {
				t.Errorf("%s.%s: no json tag", k, f.Name)
			}
		}
	}
}

func TestEveryKindRoundTripsThroughTheStore(t *testing.T) {
	s := openStore(t)
	clock := t0
	rec := audit.New(s, audit.Options{Clock: func() time.Time { clock = clock.Add(time.Second); return clock }})
	ctx := t.Context()
	for _, k := range audit.Kinds() {
		if err := rec.Record(ctx, audit.ActorCLI, "app", "j_1", samples[k]); err != nil {
			t.Fatalf("Record %s: %v", k, err)
		}
	}
	events, err := s.ListAudit(ctx, job.AuditFilter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(audit.Kinds()) {
		t.Fatalf("%d events stored, want %d", len(events), len(audit.Kinds()))
	}
	for i, e := range events {
		k := audit.Kinds()[i]
		if e.Kind != string(k) || e.ApplicationID != "app" || e.Actor != "cli" || e.JobID != "j_1" || e.At.IsZero() {
			t.Errorf("event %d = %+v", i, e)
		}
		got, err := audit.Decode(e)
		if err != nil {
			t.Errorf("Decode %s: %v", k, err)
			continue
		}
		if !reflect.DeepEqual(got, samples[k]) {
			t.Errorf("%s round trip = %+v, want %+v", k, got, samples[k])
		}
	}
}

func TestDecodeRefusesUnknownKindsAndFields(t *testing.T) {
	if _, err := audit.Decode(job.AuditEvent{Kind: "job.peeked"}); !errors.Is(err, audit.ErrUnknownKind) {
		t.Errorf("unknown kind: %v", err)
	}
	if _, err := audit.Decode(job.AuditEvent{Kind: "auth.failed", Details: json.RawMessage(`{"reason":"x","token":"y"}`)}); err == nil {
		t.Error("undeclared field accepted")
	}
	if d, err := audit.Decode(job.AuditEvent{Kind: "app.enabled"}); err != nil || d != (audit.AppEnabled{}) {
		t.Errorf("empty details = %v, %v", d, err)
	}
}

func TestJobHelpers(t *testing.T) {
	s := openStore(t)
	rec := audit.New(s, audit.Options{Clock: func() time.Time { return t0 }})
	sweeper := rec.WithActor(audit.ActorSweeper)
	ctx := t.Context()
	j := job.Job{
		ID: "j_01", ApplicationID: "app", Status: job.StatusFailed, Attempts: 2, Retention: job.RetentionNone,
		MediaType: "text/plain; charset=utf-8", Size: 42, PageCount: 0, ServiceVersion: "vde-test", CreatedAt: t0.Add(-time.Minute),
		Error: json.RawMessage(`{"code":"timeout","message":"The job's time limit expired."}`),
	}
	rec.JobCreated(ctx, j)
	sweeper.JobFinished(ctx, j)
	rec.JobDeleted(ctx, j, audit.DeletedByRequest)
	sweeper.JobExpired(ctx, j)

	events, err := s.ListAudit(ctx, job.AuditFilter{JobID: "j_01"}, 0)
	if err != nil || len(events) != 4 {
		t.Fatalf("events = %d, %v", len(events), err)
	}
	want := []struct {
		kind  audit.Kind
		actor string
		d     audit.Details
	}{
		{audit.JobCreatedKind, "", audit.JobCreated{CreatedAt: j.CreatedAt, MediaType: j.MediaType, Size: 42, Retention: "none", ServiceVersion: "vde-test"}},
		{audit.JobFinishedKind, "sweeper", audit.JobFinished{Status: "failed", Attempts: 2, ErrorCode: "timeout", ServiceVersion: "vde-test", Verification: "none", JudgePolicy: "always"}},
		{audit.JobDeletedKind, "", audit.JobDeleted{Reason: "request", Retention: "none"}},
		{audit.JobExpiredKind, "sweeper", audit.JobExpired{Status: "failed", Retention: "none"}},
	}
	for i, w := range want {
		got, err := audit.Decode(events[i])
		if events[i].Kind != string(w.kind) || events[i].Actor != w.actor || err != nil || !reflect.DeepEqual(got, w.d) {
			t.Errorf("event %d = %+v (%v), want %s %+v", i, events[i], err, w.kind, w.d)
		}
	}
}

// Content in the job row never reaches the details or the log line.
func TestNoContentInEventsOrLogs(t *testing.T) {
	s := openStore(t)
	var logs bytes.Buffer
	rec := audit.New(s, audit.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Clock: func() time.Time { return t0 }})
	const marker = "SENTINEL"
	j := job.Job{
		ID: "j_01", ApplicationID: "app", Status: job.StatusAccepted, Attempts: 1, Retention: job.RetentionDefault,
		MediaType: "application/pdf", ServiceVersion: "vde-test", CreatedAt: t0,
		Result:                json.RawMessage(`{"name":"` + marker + `"}`),
		Feedback:              json.RawMessage(`[{"note":"` + marker + `"}]`),
		Error:                 json.RawMessage(`{"code":"internal","message":"` + marker + `"}`),
		ExtractorInstructions: marker, JudgeInstructions: marker, Schema: marker, IdempotencyKey: marker, BodyHash: marker,
	}
	ctx := t.Context()
	rec.JobCreated(ctx, j)
	rec.JobFinished(ctx, j)
	rec.JobDeleted(ctx, j, audit.DeletedFirstRead)
	rec.JobExpired(ctx, j)
	events, err := s.ListAudit(ctx, job.AuditFilter{}, 0)
	if err != nil || len(events) != 4 {
		t.Fatalf("events = %d, %v", len(events), err)
	}
	for _, e := range events {
		if strings.Contains(string(e.Details), marker) {
			t.Errorf("content in %s details: %s", e.Kind, e.Details)
		}
	}
	if strings.Contains(logs.String(), marker) {
		t.Errorf("content in the log:\n%s", logs.String())
	}
}

func TestLogLine(t *testing.T) {
	s := openStore(t)
	var logs bytes.Buffer
	rec := audit.New(s, audit.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Clock: func() time.Time { return t0 }})
	if err := rec.Record(t.Context(), audit.ActorAPI, "app", "", audit.AuthFailed{Reason: "revoked"}); err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("log line %q: %v", logs.String(), err)
	}
	if line["audit"] != true || line["kind"] != "auth.failed" || line["level"] != "INFO" || line["application_id"] != "app" ||
		line["actor"] != "api" || line["details"] != `{"reason":"revoked"}` {
		t.Errorf("log line = %v", line)
	}

	// Without a logger only the table is written.
	quiet := audit.New(s, audit.Options{})
	logs.Reset()
	if err := quiet.Record(t.Context(), "", "app", "", audit.AppEnabled{}); err != nil || logs.Len() != 0 {
		t.Errorf("quiet recorder: %v, log %q", err, logs.String())
	}
}

type failingStore struct{}

func (failingStore) AppendAudit(_ context.Context, _ job.AuditEvent) (int64, error) {
	return 0, errors.New("disk full")
}

func TestStoreFailureIsReportedNotFatalForHelpers(t *testing.T) {
	var logs bytes.Buffer
	rec := audit.New(failingStore{}, audit.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if err := rec.Record(t.Context(), "", "app", "", audit.AppEnabled{}); err == nil {
		t.Error("Record hid the store error")
	}
	rec.JobFinished(t.Context(), job.Job{ID: "j_1", ApplicationID: "app", Status: job.StatusAccepted})
	if !strings.Contains(logs.String(), "audit event not written") || strings.Contains(logs.String(), `"audit":true`) {
		t.Errorf("log = %s", logs.String())
	}
}

func TestNilRecorderDiscards(t *testing.T) {
	var rec *audit.Recorder
	if err := rec.Record(t.Context(), "", "app", "", audit.AppEnabled{}); err != nil {
		t.Error(err)
	}
	rec.WithActor("x").JobFinished(t.Context(), job.Job{})
	rec.JobCreated(t.Context(), job.Job{})
	rec.JobDeleted(t.Context(), job.Job{}, audit.DeletedByRequest)
	rec.JobExpired(t.Context(), job.Job{})
}

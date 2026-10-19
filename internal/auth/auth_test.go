package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const maxTTL = 730 * 24 * time.Hour

func openStore(t *testing.T, apps ...string) *job.DB {
	t.Helper()
	s, err := job.Open(filepath.Join(t.TempDir(), "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, id := range apps {
		app := job.Application{ID: id, MaxAttempts: 3, QuotaLimit: 10, QuotaWindow: "P1D",
			RequestsPerMinute: 1, SubmitsPerMinute: 1, MaxInProgress: 1, RetentionDefault: "24h"}
		if err := s.UpsertApplication(context.Background(), app, t0); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func create(t *testing.T, s *job.DB, app string, scopes []string, expires time.Time) (string, job.Token) {
	t.Helper()
	token, row, err := Create(context.Background(), s, Request{ApplicationID: app, Scopes: scopes, ExpiresAt: expires}, maxTTL, t0)
	if err != nil {
		t.Fatal(err)
	}
	return token, row
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStore(t, "app", "gone")
	good, goodRow := create(t, s, "app", []string{ScopeJobs}, t0.Add(time.Hour))
	expiring, _ := create(t, s, "app", []string{ScopeJobs}, t0.Add(time.Minute))
	revoked, revokedRow := create(t, s, "app", []string{ScopeJobs}, t0.Add(time.Hour))
	if err := s.RevokeToken(ctx, revokedRow.ID, t0); err != nil {
		t.Fatal(err)
	}
	disabled, _ := create(t, s, "gone", []string{ScopeJobs}, t0.Add(time.Hour))
	if _, err := s.DisableApplicationsNotIn(ctx, []string{"app"}, t0); err != nil {
		t.Fatal(err)
	}
	wrongSecret := good[:len(good)-2] + "AA"
	if wrongSecret == good {
		wrongSecret = good[:len(good)-2] + "QQ"
	}
	unknown, _, _, _ := Generate()

	p, err := Authenticate(ctx, s, good, t0)
	if err != nil || p.Application.ID != "app" || p.Token.ID != goodRow.ID {
		t.Fatalf("Authenticate(good) = %+v, %v", p, err)
	}

	tests := []struct {
		name    string
		token   string
		now     time.Time
		want    Reason
		wantApp string
	}{
		{"missing", "", t0, ReasonMissing, ""},
		{"malformed", "vde_nope", t0, ReasonMalformed, ""},
		{"unknown id", unknown, t0, ReasonUnknown, ""},
		{"wrong secret", wrongSecret, t0, ReasonWrongSecret, "app"},
		{"revoked", revoked, t0, ReasonRevoked, "app"},
		{"expired at the instant", expiring, t0.Add(time.Minute), ReasonExpired, "app"},
		{"expired", good, t0.Add(2 * time.Hour), ReasonExpired, "app"},
		{"disabled application", disabled, t0, ReasonDisabled, "gone"},
	}
	for _, tt := range tests {
		_, err := Authenticate(ctx, s, tt.token, tt.now)
		var re *RefusedError
		if !errors.As(err, &re) || re.Reason != tt.want || re.ApplicationID != tt.wantApp {
			t.Errorf("%s: Authenticate = %v, want refusal %s for application %q", tt.name, err, tt.want, tt.wantApp)
		}
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStore(t, "app", "gone")
	if _, err := s.DisableApplicationsNotIn(ctx, []string{"app"}, t0); err != nil {
		t.Fatal(err)
	}
	ok := Request{ApplicationID: "app", Scopes: []string{ScopeJobs}, ExpiresAt: t0.Add(24 * time.Hour), Label: "ci"}
	tests := []struct {
		name    string
		edit    func(*Request)
		wantErr string
	}{
		{"no scope", func(r *Request) { r.Scopes = nil }, "at least one scope"},
		{"unknown scope", func(r *Request) { r.Scopes = []string{"owner"} }, "unknown scope"},
		{"admin scope with an application", func(r *Request) { r.Scopes = []string{ScopeAdmin} }, "cannot be combined"},
		{"admin scope with jobs", func(r *Request) { r.ApplicationID, r.Scopes = "", []string{ScopeAdmin, ScopeJobs} }, "cannot be combined"},
		{"no application", func(r *Request) { r.ApplicationID = "" }, "application is required"},
		{"no expiry", func(r *Request) { r.ExpiresAt = time.Time{} }, "in the future"},
		{"past expiry", func(r *Request) { r.ExpiresAt = t0.Add(-time.Second) }, "in the future"},
		{"beyond the cap", func(r *Request) { r.ExpiresAt = t0.Add(maxTTL + time.Second) }, "max_token_ttl"},
		{"unknown application", func(r *Request) { r.ApplicationID = "nope" }, "not in the database"},
		{"disabled application", func(r *Request) { r.ApplicationID = "gone" }, "disabled"},
		{"long label", func(r *Request) { r.Label = strings.Repeat("x", maxLabelChars+1) }, "label"},
	}
	for _, tt := range tests {
		req := ok
		tt.edit(&req)
		if _, _, err := Create(ctx, s, req, maxTTL, t0); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: Create error = %v, want one containing %q", tt.name, err, tt.wantErr)
		}
	}

	token, row, err := Create(ctx, s, ok, maxTTL, t0)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetTokenByID(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, _ := Parse(token)
	if !Verify(secret, stored.Hash) || stored.Label != "ci" || !stored.ExpiresAt.Equal(ok.ExpiresAt) {
		t.Errorf("stored token = %+v, want the hash of the printed secret, label and expiry", stored)
	}
	adminToken, adminRow, err := Create(ctx, s, Request{Scopes: []string{ScopeAdmin}, ExpiresAt: t0.Add(time.Hour)}, maxTTL, t0)
	if err != nil || adminRow.ApplicationID != "" {
		t.Fatalf("admin token: %+v, %v", adminRow, err)
	}
	p, err := Authenticate(ctx, s, adminToken, t0)
	if err != nil || p.Application.ID != "" || !Permits(p.Token.Scopes, ScopeAdmin) || Permits(p.Token.Scopes, "") ||
		Permits(p.Token.Scopes, ScopeJobsRead) {
		t.Errorf("admin principal = %+v, %v; want no application and the admin scope only", p, err)
	}
	if admins, err := s.ListTokens(ctx, ""); err != nil || len(admins) != 1 || admins[0].ID != adminRow.ID {
		t.Errorf("ListTokens(\"\") = %v, %v; want the admin token", admins, err)
	}
	atCap := ok
	atCap.ExpiresAt = t0.Add(maxTTL)
	if _, _, err := Create(ctx, s, atCap, maxTTL, t0); err != nil {
		t.Errorf("Create at exactly max_token_ttl: %v", err)
	}
}

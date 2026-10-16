package auth

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGenerateParseVerify(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 50 {
		token, id, hash, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) != tokenChars || !strings.HasPrefix(token, Prefix+id+"_") {
			t.Fatalf("token %q does not have the vde_<id>_<secret> shape for id %q", token, id)
		}
		gotID, secret, err := Parse(token)
		if err != nil || gotID != id {
			t.Fatalf("Parse(%q) = %q, %v; want id %q", token, gotID, err, id)
		}
		if !Verify(secret, hash) {
			t.Fatal("Verify(secret, hash) = false for the generated pair")
		}
		other := "A"
		if strings.HasSuffix(secret, "A") {
			other = "Q"
		}
		if Verify(secret[:len(secret)-1]+other, hash) {
			t.Fatal("Verify accepted a changed secret")
		}
		if seen[token] {
			t.Fatal("Generate repeated a token")
		}
		seen[token] = true
	}
}

func TestParseMalformed(t *testing.T) {
	t.Parallel()
	good, _, _, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"empty":           "",
		"no prefix":       strings.TrimPrefix(good, Prefix),
		"other prefix":    "xyz_" + good[4:],
		"short":           good[:len(good)-1],
		"long":            good + "A",
		"upper id":        Prefix + strings.ToUpper(good[4:12]) + good[12:],
		"id with 8":       Prefix + "abcdefg8" + good[12:],
		"missing _":       good[:12] + "-" + good[13:],
		"secret not b64":  good[:13] + strings.Repeat("!", secretChars),
		"padded secret":   good[:len(good)-1] + "=",
		"bearer included": "Bearer " + good,
		"space inside":    good[:20] + " " + good[21:],
	} {
		if _, _, err := Parse(token); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: Parse(%q) error = %v, want ErrMalformed", name, token, err)
		}
	}
}

func TestParseScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "jobs", want: []string{"jobs"}},
		{in: "jobs:read", want: []string{"jobs:read"}},
		{in: "jobs:read, jobs,jobs", want: []string{"jobs", "jobs:read"}},
		{in: "", wantErr: true},
		{in: "jobs,", wantErr: true},
		{in: "admin", wantErr: true},
		{in: "JOBS", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseScopes(tt.in)
		if (err != nil) != tt.wantErr || !slices.Equal(got, tt.want) {
			t.Errorf("ParseScopes(%q) = %q, %v; want %q, error %t", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestPermits(t *testing.T) {
	t.Parallel()
	jobs, read := []string{ScopeJobs}, []string{ScopeJobsRead}
	tests := []struct {
		scopes []string
		need   string
		want   bool
	}{
		{jobs, ScopeJobs, true},
		{jobs, ScopeJobsRead, true},
		{jobs, "", true},
		{read, ScopeJobsRead, true},
		{read, "", true},
		{read, ScopeJobs, false},
		{nil, "", false},
		{nil, ScopeJobsRead, false},
		{[]string{ScopeAdmin}, ScopeAdmin, true},
		{[]string{ScopeAdmin}, "", false},
		{[]string{ScopeAdmin}, ScopeJobsRead, false},
		{jobs, ScopeAdmin, false},
	}
	for _, tt := range tests {
		if got := Permits(tt.scopes, tt.need); got != tt.want {
			t.Errorf("Permits(%q, %q) = %t, want %t", tt.scopes, tt.need, got, tt.want)
		}
	}
}

func TestParseExpires(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2027-10-16", time.Date(2027, 10, 16, 0, 0, 0, 0, time.UTC)},
		{"2027-10-16T08:30:00+02:00", time.Date(2027, 10, 16, 6, 30, 0, 0, time.UTC)},
		{"90d", now.Add(90 * 24 * time.Hour)},
		{"2160h", now.Add(2160 * time.Hour)},
		{"1h30m", now.Add(90 * time.Minute)},
	}
	for _, tt := range tests {
		got, err := ParseExpires(tt.in, now)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("ParseExpires(%q) = %s, %v; want %s", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "never", "-1h", "0d", "d", "2027-13-01", "16.10.2027"} {
		if _, err := ParseExpires(bad, now); err == nil {
			t.Errorf("ParseExpires(%q) accepted", bad)
		}
	}
}

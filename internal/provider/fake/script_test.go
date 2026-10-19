package fake_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

func TestHelpers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script fake.Script
		want   []string
	}{
		{"accept first try", fake.Script{fake.Answer("c"), fake.Answer(fake.AcceptVerdict)}, []string{"c", fake.AcceptVerdict, "error permanent 0"}},
		{"schema invalid then valid", fake.Script{fake.Answer("bad"), fake.Answer("good"), fake.Answer(fake.AcceptVerdict)}, []string{"bad", "good", fake.AcceptVerdict}},
		{"reject then accept", fake.Script{fake.Answer("c1"), fake.Answer(fake.RejectVerdict), fake.Answer("c2"), fake.Answer(fake.AcceptVerdict)}, []string{"c1", fake.RejectVerdict, "c2", fake.AcceptVerdict}},
		{"always retryable", fake.AlwaysRetryable(503), []string{"error retryable 503", "error retryable 503"}},
		{"always permanent", fake.AlwaysPermanent(403), []string{"error permanent 403", "error permanent 403"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := fake.New(tt.script)
			for i, want := range tt.want {
				resp, err := p.Complete(context.Background(), provider.Request{})
				if got := outcome(resp, err); got != want {
					t.Errorf("call %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		want    fake.Script
		wantErr string
	}{
		{
			name: "all step kinds",
			yaml: `
description: d
steps:
  - {role: judge, model: m, delay: 5ms, answer: ""}
  - error: {kind: retryable, status: 429, retry_after: 2s}
    repeat: true
  - error: {kind: permanent}
  - {answer: cut, truncated: true}
`,
			want: fake.Script{
				{Role: "judge", Model: "m", Delay: 5 * time.Millisecond},
				{Repeat: true, Err: &provider.Error{Kind: provider.Retryable, Status: 429, RetryAfter: 2 * time.Second, Message: "fake: scripted failure"}},
				{Err: &provider.Error{Kind: provider.Permanent, Message: "fake: scripted failure"}},
				{Answer: "cut", Truncated: true},
			},
		},
		{name: "empty", yaml: "", wantErr: "script is empty"},
		{name: "no steps", yaml: "steps: []\n", wantErr: "no steps"},
		{name: "unknown key", yaml: "steps:\n  - {anwser: x}\n", wantErr: "field anwser not found"},
		{name: "neither answer nor error", yaml: "steps:\n  - {delay: 1s}\n", wantErr: "steps[0]: exactly one of answer or error"},
		{name: "both answer and error", yaml: "steps:\n  - {answer: x, error: {kind: permanent}}\n", wantErr: "exactly one"},
		{name: "bad kind", yaml: "steps:\n  - error: {kind: sometimes}\n", wantErr: `steps[0].error.kind: "sometimes"`},
		{name: "negative delay", yaml: "steps:\n  - {delay: -1s, answer: x}\n", wantErr: "steps[0].delay"},
		{name: "bad duration", yaml: "steps:\n  - {delay: soon, answer: x}\n", wantErr: "cannot unmarshal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := fake.Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Parse() = %d steps, want %d", len(got), len(tt.want))
			}
			for i := range got {
				g, w := got[i], tt.want[i]
				if g.Role != w.Role || g.Model != w.Model || g.Delay != w.Delay || g.Answer != w.Answer || g.Repeat != w.Repeat || g.Truncated != w.Truncated ||
					(g.Err == nil) != (w.Err == nil) || g.Err != nil && *g.Err != *w.Err {
					t.Errorf("step %d = %+v (err %+v), want %+v (err %+v)", i, g, g.Err, w, w.Err)
				}
			}
		})
	}
}

func TestLoad_scenarios(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("../../../testdata/scenarios/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no scenarios found: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			p, err := fake.Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if p.Remaining() == 0 {
				t.Error("Remaining() = 0, want consumable steps")
			}
		})
	}
}

func TestLoad_extractorPrimaryDown(t *testing.T) {
	t.Parallel()
	p, err := fake.Load("../../../testdata/scenarios/extractor-primary-down.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, model := range []string{"primary-model", "fallback-model", "judge-model"} {
		resp, err := p.Complete(context.Background(), provider.Request{Model: model})
		got = append(got, outcome(resp, err))
	}
	want := []string{"error retryable 503", `{"guest": "Jane Example", "nights": 3}`, fake.AcceptVerdict}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("outcomes = %q, want %q", got, want)
	}
}

func TestLoad_errors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("steps: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.yaml"), bad} {
		if _, err := fake.Load(path); err == nil {
			t.Errorf("Load(%s) error = nil, want an error", filepath.Base(path))
		}
	}
}

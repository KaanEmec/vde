package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line %q is not JSON: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestAllowedKeys(t *testing.T) {
	t.Parallel()
	got := allowed
	// The keys the code logs with today and the keys the contract's job file lists.
	for _, want := range []string{
		"job_id", "app_id", "application_id", "request_id", "status", "attempts", "endpoint",
		"model", "duration_ms", "media_type", "bytes", "pages", "error_code", "method",
		"route", "worker_id", "role", "kind", "outcome", "count", "verification", "service_version",
		"component", "error", "reason", "panic", "stack", "version", "listen", "tls",
		"trusted_proxy", "drain_timeout", "msg", "time", "level",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("allowed lacks %q", want)
		}
	}
	// Content-bearing names must never be added.
	for _, bad := range []string{
		"document", "text", "instructions", "extractor_instructions", "judge_instructions",
		"schema", "candidate", "result", "feedback", "body", "response", "prompt", "answer",
		"filename", "token", "authorization", "api_key",
	} {
		if slices.Contains(got, bad) {
			t.Errorf("allowed contains content or secret key %q", bad)
		}
	}
}

func TestNewDropsUnknownKeys(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo, "json").With("document", "SECRET-WITH", "job_id", "j1")
	l.Info("hello", "candidate", "SECRET-ATTR", "status", "accepted", "attempts", 2,
		slog.Group("grp", slog.String("schema", "SECRET-GROUP"), slog.String("endpoint", "primary")),
		"nested", map[string]string{"x": "SECRET-MAP"})
	if strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("a dropped attribute leaked: %s", buf.String())
	}
	got := lines(t, &buf)
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	m := got[0]
	if m["msg"] != "hello" || m["job_id"] != "j1" || m["status"] != "accepted" || m["attempts"] != float64(2) {
		t.Errorf("allowed attributes missing: %v", m)
	}
	if m["level"] != "INFO" || m["time"] == nil {
		t.Errorf("standard keys missing: %v", m)
	}
	if g, _ := m["grp"].(map[string]any); g["endpoint"] != "primary" || g["schema"] != nil {
		t.Errorf("group = %v, want only endpoint", m["grp"])
	}
}

func TestNewLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := New(&buf, slog.LevelWarn, "json")
	l.Info("quiet")
	l.Warn("loud")
	got := lines(t, &buf)
	if len(got) != 1 || got[0]["msg"] != "loud" {
		t.Errorf("lines = %v, want only the warning", got)
	}
}

func TestNewTruncates(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", MaxValueLen+50) // multibyte: bytes exceed runes
	exact := strings.Repeat("x", MaxValueLen)
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo, "json")
	l.Info(long, "reason", long, "error", errors.New(long), "route", exact,
		"stack", strings.Repeat("s", 1000))
	m := lines(t, &buf)[0]
	for _, k := range []string{"msg", "reason", "error"} {
		s, _ := m[k].(string)
		if want := strings.Repeat("é", MaxValueLen) + truncated; s != want {
			t.Errorf("%s = %d chars, want the first %d plus the marker", k, len([]rune(s)), MaxValueLen)
		}
	}
	if m["route"] != exact {
		t.Error("a value of exactly the limit was cut")
	}
	if s, _ := m["stack"].(string); len(s) != 1000 {
		t.Errorf("stack was cut to %d, want it whole under %d", len(s), MaxStackLen)
	}
}

type stringer struct{}

func (stringer) String() string { return strings.Repeat("a", 300) }

func TestNewRendersAnyValues(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo, "json")
	l.Info("m", "error", &json.SyntaxError{Offset: 3}, "reason", stringer{}, "panic", struct{ A string }{"SECRET-STRUCT"})
	if strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("a struct value leaked: %s", buf.String())
	}
	m := lines(t, &buf)[0]
	if s, _ := m["reason"].(string); len([]rune(s)) != MaxValueLen+len(truncated) {
		t.Errorf("a Stringer value was not truncated: %d", len(s))
	}
	if m["error"] == nil {
		t.Error("an error value was dropped")
	}
}

func TestNewText(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo, "text")
	l.Info("hello", "job_id", "j1", "body", "SECRET-TEXT")
	out := buf.String()
	if strings.Contains(out, "SECRET") || !strings.Contains(out, "job_id=j1") || !strings.HasPrefix(out, "time=") {
		t.Errorf("text output = %q", out)
	}
}

func TestNewUnknownFormatIsJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo, "").Info("x")
	if len(lines(t, &buf)) != 1 {
		t.Errorf("output %q is not JSON", buf.String())
	}
}

func TestContentLogger(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "content.log")
	cl, err := OpenContentLog(path)
	if err != nil {
		t.Fatal(err)
	}
	cl.Log(context.Background(), "extractor request", slog.String("job_id", "j1"), slog.String("document", "SECRET-CONTENT"))
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SECRET-CONTENT") || !strings.Contains(string(data), `"level":"DEBUG"`) {
		t.Errorf("content log = %q", data)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestContentLoggerRefusesStreams(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "-", "/dev/stdout", "/dev/stderr", "/dev/fd/1", "/dev/fd/2"} {
		if cl, err := OpenContentLog(p); err == nil {
			_ = cl.Close()
			t.Errorf("OpenContentLog(%q) succeeded", p)
		}
	}
	link := filepath.Join(t.TempDir(), "out")
	if err := os.Symlink("/dev/stdout", link); err == nil {
		if cl, err := OpenContentLog(link); err == nil {
			_ = cl.Close()
			t.Error("a symlink to stdout was accepted")
		}
	}
}

func TestNilContentLogger(t *testing.T) {
	t.Parallel()
	var cl *ContentLogger
	cl.Log(context.Background(), "x", slog.String("a", "b"))
	if err := cl.Close(); err != nil {
		t.Error(err)
	}
}

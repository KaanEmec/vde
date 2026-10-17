package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// TestServeEndToEnd runs vde serve on a loopback port with the fake provider replaying
// a scenario script and a token made with the CLI: healthz, quota, an unknown job, a
// retention: none submission polled to accepted, the first-read deletion (contract §14
// item 8), and the metrics a scrape shows afterwards.
func TestServeEndToEnd(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := writeServiceConfig(t, dataDir, "app-a")
	script, err := fake.Load(filepath.Join(scenarioDir, "accept-first-try.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	base, stop := startService(t, path, script)
	code, token, stderr := cli("token", "create", "--config", path, "--app", "app-a", "--scope", "jobs", "--expires", "1d")
	if code != exitOK {
		t.Fatalf("token create = %d: %s", code, stderr)
	}
	token = strings.TrimSpace(token)

	if r := call(t, http.MethodGet, base+"/healthz", "", nil, ""); r.status != http.StatusOK ||
		strings.TrimSpace(r.body) != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %s", r.status, r.body)
	}
	if r := call(t, http.MethodGet, base+"/v1/jobs/j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", token, nil, ""); r.status != http.StatusNotFound {
		t.Errorf("unknown job = %d %s, want 404", r.status, r.body)
	}
	resp := call(t, http.MethodGet, base+"/v1/quota", token, nil, "")
	body := resp.body
	var quota struct {
		Limit, Remaining int
		Reset, Window    string
	}
	if err := json.Unmarshal([]byte(body), &quota); err != nil || resp.status != http.StatusOK ||
		quota.Limit != 1000 || quota.Remaining != 1000 || quota.Window != "P1D" ||
		resp.header.Get("X-Quota-Remaining") != "1000" || resp.header.Get("X-Quota-Reset") != quota.Reset {
		t.Fatalf("GET /v1/quota = %d %s (headers %v)", resp.status, body, resp.header)
	}

	form, contentType := submission(t)
	resp = call(t, http.MethodPost, base+"/v1/jobs", token, form, contentType)
	body = resp.body
	if resp.status != http.StatusAccepted && resp.status != http.StatusCreated && resp.status != http.StatusOK {
		t.Fatalf("POST /v1/jobs = %d %s", resp.status, body)
	}
	var created struct{ ID, Status string }
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" {
		t.Fatalf("submit body %s: %v", body, err)
	}

	var final map[string]json.RawMessage
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		resp = call(t, http.MethodGet, base+"/v1/jobs/"+created.ID, token, nil, "")
		body = resp.body
		if resp.status != http.StatusOK {
			t.Fatalf("poll = %d %s", resp.status, body)
		}
		if err := json.Unmarshal([]byte(body), &final); err != nil {
			t.Fatal(err)
		}
		if s := string(final["status"]); s != `"queued"` && s != `"running"` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s after 10 s", final["status"])
		}
	}
	if string(final["status"]) != `"accepted"` || !strings.Contains(string(final["result"]), "Jane Example") ||
		string(final["attempts"]) != "1" || string(final["error"]) != "null" || string(final["feedback"]) != "[]" {
		t.Errorf("terminal job = %s", body)
	}
	if resp.header.Get("X-Service-Version") == "" || string(final["service_version"]) != `"`+resp.header.Get("X-Service-Version")+`"` {
		t.Errorf("service_version %s, header %q", final["service_version"], resp.header.Get("X-Service-Version"))
	}
	// retention: none — the terminal read above was the first: the job is gone now.
	if r := call(t, http.MethodGet, base+"/v1/jobs/"+created.ID, token, nil, ""); r.status != http.StatusNotFound {
		t.Errorf("second terminal read = %d, want 404", r.status)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "blobs", created.ID[len(created.ID)-2:], created.ID)); !os.IsNotExist(err) {
		t.Errorf("the job's blob directory survives the first read: %v", err)
	}
	if r := call(t, http.MethodGet, base+"/v1/quota", token, nil, ""); !strings.Contains(r.body, `"remaining":999`) {
		t.Errorf("quota after one submission and polls: %s, want remaining 999", r.body)
	}

	metricsText := call(t, http.MethodGet, base+"/metrics", "", nil, "").body
	for _, want := range []string{
		`vde_jobs_total{app="app-a",status="accepted"} 1`,
		`vde_provider_calls_total{endpoint="scripted",group="main",model="m",outcome="ok",role="extractor"} 1`,
		`vde_provider_calls_total{endpoint="scripted",group="main",model="m",outcome="ok",role="judge"} 1`,
		`vde_provider_tokens_total{endpoint="scripted",group="main",kind="input",model="m",role="extractor"} 100`,
		`vde_provider_tokens_total{endpoint="scripted",group="main",kind="output",model="m",role="judge"} 20`,
		`vde_endpoint_up{endpoint="scripted",group="main",instance="`,
		`vde_http_requests_total{route="GET /v1/jobs/{id}",status="404"}`,
		`vde_quota_remaining{app="app-a"} 999`,
	} {
		if !strings.Contains(metricsText, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	if strings.Contains(metricsText, "Jane Example") || strings.Contains(metricsText, created.ID) {
		t.Error("metrics carry content or a job id")
	}
	stop()
}

// submission is a retention: none text job with the sample instructions and no schema.
func submission(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part := func(name, file, contentType, content string) {
		h := textproto.MIMEHeader{}
		disposition := `form-data; name="` + name + `"`
		if file != "" {
			disposition += `; filename="` + file + `"`
		}
		h.Set("Content-Disposition", disposition)
		if contentType != "" {
			h.Set("Content-Type", contentType)
		}
		p, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(p, content)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(samples, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	part("document", "booking.txt", "text/plain; charset=utf-8", read("booking.txt"))
	part("extractor_instructions", "extractor.md", "text/markdown", read("extractor.md"))
	part("judge_instructions", "judge.md", "text/markdown", read("judge.md"))
	part("retention", "", "", "none")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

// reply is a response read whole.
type reply struct {
	status int
	header http.Header
	body   string
}

func call(t *testing.T, method, url, token string, body io.Reader, contentType string) reply {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(data)}
}

// policyServiceConfig: a text-only extractor (PDFs reach it as their text layer, ADR
// 0010) and a judge whose every call fails, one application per judge policy.
const policyServiceConfig = `
server: {data_dir: "DATA"}
endpoints:
  ext:
    provider: fake
    models: {ext-model: {accepts: [text/plain]}}
  judge:
    provider: fake
    models: {judge-model: {accepts: [text/plain]}}
groups:
  extraction: {endpoints: [{endpoint: ext, model: ext-model}]}
  review: {endpoints: [{endpoint: judge, model: judge-model}], failover: {max_tries: 1, max_total_time: 5s}}
roles: {extractor: {group: extraction}, judge: {group: review}}
applications:
  - {id: app-auto, judge: {policy: auto, sample_rate: 0}}
  - {id: app-never, judge: {policy: never}}
  - {id: app-always}
`

// TestServeJudgePolicies runs vde serve with an unreachable judge (ADR 0009): under auto
// a grounded candidate is accepted without it, for a text document and for booking.pdf
// read through its text layer; never ends extracted; always cannot accept without the
// judge and fails. Status, verification and the judge decision metrics are checked over
// HTTP.
func TestServeJudgePolicies(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "vde.yaml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(policyServiceConfig, "DATA", dataDir)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadConfigEnv(path, false, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ext := fake.New(fake.Script{{Answer: `{"guest": "Jane Example", "nights": 3}`, Repeat: true}})
	judge := fake.New(fake.AlwaysRetryable(503))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, cfg, ln, nil, slog.New(slog.DiscardHandler), func(e *config.Endpoint) (provider.Provider, error) {
			if e == cfg.Endpoints["judge"] {
				return judge, nil
			}
			return ext, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	base := "http://" + ln.Addr().String()
	waitHealthz(t, base)
	tokens := map[string]string{}
	for _, app := range []string{"app-auto", "app-never", "app-always"} {
		code, token, stderr := cli("token", "create", "--config", path, "--app", app, "--scope", "jobs", "--expires", "1d")
		if code != exitOK {
			t.Fatalf("token create = %d: %s", code, stderr)
		}
		tokens[app] = strings.TrimSpace(token)
	}

	for _, tt := range []struct {
		app, file, mediaType     string
		wantStatus, verification string
	}{
		{"app-auto", "booking.txt", "text/plain; charset=utf-8", "accepted", "grounded"},
		{"app-auto", "booking.pdf", "application/pdf", "accepted", "grounded"},
		{"app-never", "booking.txt", "text/plain; charset=utf-8", "extracted", "none"},
	} {
		final := submitAndWait(t, base, tokens[tt.app], tt.file, tt.mediaType)
		if string(final["status"]) != `"`+tt.wantStatus+`"` || string(final["verification"]) != `"`+tt.verification+`"` ||
			string(final["attempts"]) != "1" || string(final["feedback"]) != "[]" {
			t.Errorf("%s %s: terminal job = %v", tt.app, tt.file, final)
		}
	}
	if n := len(judge.Calls()); n != 0 {
		t.Errorf("the judge was called %d times under auto and never", n)
	}
	var sawPDFText bool
	for _, c := range ext.Calls() {
		for _, p := range c.Request.Parts {
			sawPDFText = sawPDFText || strings.Contains(p.Text, "--- page 1 ---")
		}
	}
	if !sawPDFText {
		t.Error("the extractor never read the PDF's text layer")
	}

	final := submitAndWait(t, base, tokens["app-always"], "booking.txt", "text/plain; charset=utf-8")
	if string(final["status"]) != `"failed"` || string(final["verification"]) != `"none"` ||
		!strings.Contains(string(final["error"]), "provider_unavailable") {
		t.Errorf("always without a judge: terminal job = %v", final)
	}

	metricsText := call(t, http.MethodGet, base+"/metrics", "", nil, "").body
	for _, want := range []string{
		`vde_judge_decisions_total{decision="grounded",policy="auto"} 2`,
		`vde_judge_decisions_total{decision="skipped",policy="never"} 1`,
		`vde_judge_decisions_total{decision="judged",policy="always"} 1`,
		`vde_grounding_disagreements_total 0`,
	} {
		if !strings.Contains(metricsText, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}

func waitHealthz(t *testing.T, base string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not answer: %v", err)
		}
	}
}

// submitAndWait submits the sample file with the sample instructions (schema-less,
// retention default) and polls the job to its terminal state.
func submitAndWait(t *testing.T, base, token, file, mediaType string) map[string]json.RawMessage {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range []struct{ name, file, contentType string }{
		{"document", file, mediaType},
		{"extractor_instructions", "extractor.md", "text/markdown"},
		{"judge_instructions", "judge.md", "text/markdown"},
	} {
		data, err := os.ReadFile(filepath.Join(samples, p.file))
		if err != nil {
			t.Fatal(err)
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+p.name+`"; filename="`+p.file+`"`)
		h.Set("Content-Type", p.contentType)
		part, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(data)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	resp := call(t, http.MethodPost, base+"/v1/jobs", token, &buf, w.FormDataContentType())
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(resp.body), &created); err != nil || created.ID == "" || resp.status >= 300 {
		t.Fatalf("POST /v1/jobs %s = %d %s", file, resp.status, resp.body)
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r := call(t, http.MethodGet, base+"/v1/jobs/"+created.ID, token, nil, "")
		var final map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.body), &final); err != nil || r.status != http.StatusOK {
			t.Fatalf("poll = %d %s", r.status, r.body)
		}
		if s := string(final["status"]); s != `"queued"` && s != `"running"` {
			return final
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s after 20 s", final["status"])
		}
	}
}

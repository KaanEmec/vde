package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	base, _, stop := startServe(t, path, serveOptions{build: serveWith(script)})
	token := jobsToken(t, path, "app-a")

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

	resp, final := waitJob(t, base, token, created.ID, 10*time.Second)
	body = resp.body
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
		`vde_jobs_in_progress{app="app-a"} 0`,
	} {
		if !strings.Contains(metricsText, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	if strings.Contains(metricsText, "Jane Example") || strings.Contains(metricsText, created.ID) {
		t.Error("metrics carry content or a job id")
	}
	// The set of series, with their label names, is part of the operator interface.
	if got, want := seriesNames(metricsText), strings.Join([]string{
		"vde_audit_auth_failed_dropped_total{}",
		"vde_endpoint_up{endpoint,group,instance,model}",
		"vde_grounding_disagreements_total{}",
		"vde_http_requests_total{route,status}",
		"vde_job_attempts_bucket{le}",
		"vde_job_attempts_sum{}",
		"vde_job_attempts_count{}",
		"vde_job_duration_seconds_bucket{status,le}",
		"vde_job_duration_seconds_sum{status}",
		"vde_job_duration_seconds_count{status}",
		"vde_jobs_in_progress{app}",
		"vde_jobs_total{app,status}",
		"vde_judge_decisions_total{decision,policy}",
		"vde_provider_calls_total{endpoint,group,model,outcome,role}",
		"vde_provider_latency_seconds_bucket{endpoint,group,role,le}",
		"vde_provider_latency_seconds_sum{endpoint,group,role}",
		"vde_provider_latency_seconds_count{endpoint,group,role}",
		"vde_provider_tokens_total{endpoint,group,kind,model,role}",
		"vde_quota_remaining{app}",
		"vde_swept_jobs_total{}",
	}, "\n"); got != want {
		t.Errorf("series on /metrics:\n%s\nwant:\n%s", got, want)
	}
	stop()
}

var labelName = regexp.MustCompile(`(\w+)="`)

// seriesNames lists the distinct series of a Prometheus text scrape as name{label,...},
// without values, one per line in scrape order.
func seriesNames(text string) string {
	var out []string
	for line := range strings.Lines(text) {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		series := line[:strings.LastIndexByte(strings.TrimSpace(line), ' ')] // drop the value
		name, labels, _ := strings.Cut(series, "{")
		var keys []string
		for _, m := range labelName.FindAllStringSubmatch(labels, -1) {
			keys = append(keys, m[1])
		}
		s := name + "{" + strings.Join(keys, ",") + "}"
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
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
	ext := fake.New(fake.Script{{Answer: `{"guest": "Jane Example", "nights": 3}`, Repeat: true}})
	judge := fake.New(fake.AlwaysRetryable(503))
	base, _, _ := startServe(t, path, serveOptions{build: func(e *config.Endpoint) (provider.Provider, error) {
		if _, ok := e.Models["judge-model"]; ok {
			return judge, nil
		}
		return ext, nil
	}})
	tokens := map[string]string{}
	for _, app := range []string{"app-auto", "app-never", "app-always"} {
		tokens[app] = jobsToken(t, path, app)
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
	for deadline := time.Now().Add(e2eWait(5 * time.Second)); ; time.Sleep(10 * time.Millisecond) {
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
	_, final := waitJob(t, base, token, created.ID, 20*time.Second)
	return final
}

// waitJob polls GET /v1/jobs/{id} until the job is terminal, at most limit (scaled by
// e2eWait), and returns the last response and its body decoded.
func waitJob(t *testing.T, base, token, id string, limit time.Duration) (reply, map[string]json.RawMessage) {
	t.Helper()
	for deadline := time.Now().Add(e2eWait(limit)); ; time.Sleep(20 * time.Millisecond) {
		r := call(t, http.MethodGet, base+"/v1/jobs/"+id, token, nil, "")
		var job map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.body), &job); err != nil || r.status != http.StatusOK {
			t.Fatalf("poll = %d %s", r.status, r.body)
		}
		if s := string(job["status"]); s != `"queued"` && s != `"running"` {
			return r, job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s after %s", job["status"], limit)
		}
	}
}

// jobsToken creates a jobs token for app with the token CLI.
func jobsToken(t *testing.T, path, app string) string {
	t.Helper()
	code, token, stderr := cli("token", "create", "--config", path, "--app", app, "--scope", "jobs", "--expires", "1d")
	if code != exitOK {
		t.Fatalf("token create = %d: %s", code, stderr)
	}
	return strings.TrimSpace(token)
}

// e2eWait scales a wait for the race detector and shared CI runners: pdfium under -race
// with coverage is many times slower than a plain build (ADR 0010 measurements).
func e2eWait(d time.Duration) time.Duration {
	if raceEnabled {
		return d * 8
	}
	return d
}

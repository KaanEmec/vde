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
	"strings"
	"testing"
	"time"

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
	if string(final["status"]) != `"accepted"` || !strings.Contains(string(final["result"]), "Ada Lovelace") ||
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
	if strings.Contains(metricsText, "Ada") || strings.Contains(metricsText, created.ID) {
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

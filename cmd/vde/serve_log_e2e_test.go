package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServeProcessLogsThroughRedactingLogger runs vde serve as a process from the built
// binary (runServe's own wiring, no test logger) with logging.format json and an endpoint
// whose port refuses connections, submits a job, and asserts that the extraction loop's
// provider_unavailable warning, and every other line the process wrote, came through the
// redacting JSON handler: the stock slog default would write plain text (security review
// F2).
func TestServeProcessLogsThroughRedactingLogger(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	listen, dead := freeAddr(t), freeAddr(t)
	path := writeConfig(t, `
server: {listen: "`+listen+`", data_dir: "`+dataDir+`"}
admin: {listen: ""}
endpoints:
  dead:
    provider: openai_compatible
    base_url: http://`+dead+`/v1
    timeout: 2s
    models:
      m: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
groups:
  main:
    endpoints: [{endpoint: dead, model: m}]
    failover: {max_tries: 1}
roles: {extractor: {group: main}, judge: {group: main}}
documents: {pdf: {mode: native}}
logging: {level: info, format: json}
applications:
  - {id: app-a}
`)
	cmd := exec.Command(vdeBinary, "serve", "--config", path)
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	}
	t.Cleanup(stop)
	base := "http://" + listen
	for deadline := time.Now().Add(e2eWait(10 * time.Second)); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("vde serve did not start:\n%s", stderr.String())
		}
	}
	token := jobsToken(t, path, "app-a")

	form, contentType := submission(t)
	r := call(t, http.MethodPost, base+"/v1/jobs", token, form, contentType)
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(r.body), &created); err != nil || created.ID == "" {
		t.Fatalf("POST /v1/jobs = %d %s", r.status, r.body)
	}
	r, _ = waitJob(t, base, token, created.ID, 20*time.Second)
	if !strings.Contains(r.body, `"provider_unavailable"`) {
		t.Fatalf("terminal job = %d %s, want failed with provider_unavailable", r.status, r.body)
	}
	stop()

	var found bool
	for line := range strings.Lines(stderr.String()) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("a line did not come through the JSON handler: %q", line)
			continue
		}
		if rec["msg"] == "extract: endpoint group unavailable" {
			found = true
		}
	}
	if !found {
		t.Errorf("no provider_unavailable warning in the process log:\n%s", stderr.String())
	}
}

// freeAddr returns a loopback address with a port nothing listens on right now.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

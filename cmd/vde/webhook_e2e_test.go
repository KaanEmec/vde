package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/webhook"
)

// TestServeSyncExtractAndWebhook runs vde serve with an application that has a
// completion webhook pointing at an httptest receiver: POST /v1/extract answers 200
// with the accepted job in one request, and the receiver gets exactly one delivery whose
// signature verifies and whose body carries no content (v0.3 epic 05 acceptance).
func TestServeSyncExtractAndWebhook(t *testing.T) {
	const secret = "e2e-webhook-secret-0123456789"
	t.Setenv("VDE_TEST_WEBHOOK_SECRET", secret)
	deliveries := make(chan []byte, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := webhook.Verify([]byte(secret), r.Header.Get(webhook.HeaderSignature), body, time.Now(), time.Minute); err != nil {
			t.Errorf("signature: %v", err)
		}
		deliveries <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close) // before startServe: cleanups run last-first, so serve stops before the receiver closes

	dataDir := t.TempDir()
	text := strings.NewReplacer("DATA", dataDir, "APPS", "  - id: app-hook\n    webhook: {url: \""+receiver.URL+
		"/hook\", secret_env: VDE_TEST_WEBHOOK_SECRET}\n").Replace(serviceConfig)
	path := filepath.Join(t.TempDir(), "vde.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := fake.Load(filepath.Join(scenarioDir, "accept-first-try.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	base, _, _ := startServe(t, path, serveOptions{build: serveWith(script), getenv: os.Getenv})
	token := jobsToken(t, path, "app-hook")

	form, contentType := submission(t)
	resp := call(t, http.MethodPost, base+"/v1/extract", token, form, contentType)
	var job struct{ ID, Status string }
	if err := json.Unmarshal([]byte(resp.body), &job); err != nil || resp.status != http.StatusOK || job.Status != "accepted" {
		t.Fatalf("POST /v1/extract = %d %s", resp.status, resp.body)
	}
	select {
	case body := <-deliveries:
		var p webhook.Payload
		if err := json.Unmarshal(body, &p); err != nil || p.JobID != job.ID || p.Status != "accepted" ||
			p.ApplicationID != "app-hook" || p.Event != "job.finished" || p.ServiceVersion == "" || p.FinishedAt == "" {
			t.Errorf("payload = %s (%v)", body, err)
		}
		if strings.Contains(string(body), "Jane Example") {
			t.Errorf("payload carries content: %s", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no webhook delivery")
	}
	select {
	case body := <-deliveries:
		t.Errorf("a second delivery: %s", body)
	case <-time.After(200 * time.Millisecond):
	}
}

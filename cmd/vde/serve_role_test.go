package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/api"
)

func TestRunServeRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"serve", "--role", "scheduler"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `--role "scheduler" is not all, api or worker`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestServeRoles: an API-only process reports the queue age on /healthz; a worker
// process serves /healthz but not the API.
func TestServeRoles(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		role       string
		wantHealth string
		wantQuota  int // GET /v1/quota without a token
	}{
		{api.RoleAPI, `{"status":"ok","queue_age_seconds":0}`, http.StatusUnauthorized},
		{api.RoleWorker, `{"status":"ok"}`, http.StatusNotFound},
	} {
		t.Run(tt.role, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, noPDFConfig+"server: {data_dir: "+strconv.Quote(t.TempDir())+"}\n")
			cfg, _, err := loadConfigEnv(path, false, func(string) string { return "" })
			if err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var logs syncBuffer
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- serveAs(ctx, tt.role, cfg, ln, nil, slog.New(slog.NewJSONHandler(&logs, nil)), newProvider)
			}()
			base := "http://" + ln.Addr().String()
			get := func(path string) (int, string) {
				resp, err := http.Get(base + path)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				return resp.StatusCode, strings.TrimSpace(string(body))
			}
			if code, body := get("/healthz"); code != http.StatusOK || body != tt.wantHealth {
				t.Errorf("GET /healthz = %d %s, want 200 %s", code, body, tt.wantHealth)
			}
			if code, _ := get("/v1/quota"); code != tt.wantQuota {
				t.Errorf("GET /v1/quota = %d, want %d", code, tt.wantQuota)
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("serve = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("serve did not return")
			}
			if want := `"process_role":"` + tt.role + `"`; !strings.Contains(logs.String(), want) {
				t.Errorf("start log lacks %s: %s", want, logs.String())
			}
		})
	}
}

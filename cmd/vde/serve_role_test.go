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
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/router"
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

// TestHealthCoversEveryGroup: /healthz checks every group a role may use: escalation
// steps, the second judge and the feedback group, not only the primary groups.
func TestHealthCoversEveryGroup(t *testing.T) {
	t.Parallel()
	cfg, err := config.Parse([]byte(`
endpoints:
  a: {provider: fake, models: {m: {accepts: [text/plain]}}}
  b: {provider: fake, models: {m: {accepts: [text/plain]}}}
groups:
  main: {endpoints: [{endpoint: a, model: m}], failover: {max_tries: 1}}
  strong: {endpoints: [{endpoint: b, model: m}], failover: {max_tries: 1}}
  second: {endpoints: [{endpoint: b, model: m}], failover: {max_tries: 1}}
roles:
  extractor: {group: main, escalation: [{from_attempt: 2, group: strong}]}
  judge: {group: main}
  second_judge: {group: second}
`), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	down := fake.New(fake.AlwaysRetryable(503))
	up := fake.New(fake.Script{{Answer: "ok", Repeat: true}})
	roles, err := extract.NewRoles(cfg, func(e *config.Endpoint) (provider.Provider, error) {
		if e == cfg.Endpoints["b"] {
			return down, nil
		}
		return up, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	names := func(gs []*router.Group) (out []string) {
		for _, g := range gs {
			out = append(out, g.Name())
		}
		return out
	}
	if got := strings.Join(names(extractorGroups(roles)), ","); got != "main,strong" {
		t.Errorf("extractor groups = %s, want main,strong", got)
	}
	if got := strings.Join(names(judgeGroups(roles)), ","); got != "main,second" {
		t.Errorf("judge groups = %s, want main,second", got)
	}
	extractor, judge := allHealthy(extractorGroups(roles)), allHealthy(judgeGroups(roles))
	if !extractor.Healthy() || !judge.Healthy() {
		t.Fatal("unhealthy before any call")
	}
	// strong and second (both on endpoint b) fail once and are marked down.
	for _, g := range []*router.Group{roles.ExtractorEscalation[0].Group, roles.SecondJudge} {
		if _, _, err := g.Complete(t.Context(), provider.Request{Parts: []provider.Part{{Text: "x"}}}); err == nil {
			t.Fatal("a failing group answered")
		}
	}
	if extractor.Healthy() || judge.Healthy() {
		t.Error("an escalation group or the second judge marked down does not make its role unhealthy")
	}
	if (allHealthy{}).Healthy() {
		t.Error("no groups reads as healthy")
	}
}

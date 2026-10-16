package config

import (
	"strings"
	"testing"
	"time"
)

func TestServiceDefaults(t *testing.T) {
	t.Parallel()
	c := validConfig(t)
	if c.Worker.Size != 4 || c.Worker.PollInterval != 500*time.Millisecond {
		t.Errorf("worker = %+v, want size 4 and poll_interval 500ms", c.Worker)
	}
	if c.Retention.SweepInterval != time.Minute {
		t.Errorf("retention.sweep_interval = %s, want 1m", c.Retention.SweepInterval)
	}
	if c.Audit.Retention != 365*24*time.Hour || c.Audit.Log {
		t.Errorf("audit = %+v, want retention 8760h and log off", c.Audit)
	}
	if c.Metrics != (Metrics{}) {
		t.Errorf("metrics = %+v, want all unset", c.Metrics)
	}
	if c.Admin.Listen != "127.0.0.1:8091" {
		t.Errorf("admin.listen = %q, want 127.0.0.1:8091", c.Admin.Listen)
	}
}

func TestAdminListen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, server, admin string
		wantMessage         string // "" means no issue
	}{
		{name: "default", server: `{listen: ":8090"}`},
		{name: "disabled", server: `{listen: ":8090"}`, admin: `{listen: ""}`},
		{name: "IPv6 loopback", server: `{listen: ":8090"}`, admin: `{listen: "[::1]:9443"}`},
		{name: "not host:port", server: `{listen: ":8090"}`, admin: `{listen: "8091"}`, wantMessage: "host:port"},
		{name: "same as server", server: `{listen: "127.0.0.1:8090"}`, admin: `{listen: "127.0.0.1:8090"}`, wantMessage: "differ"},
		{name: "public without TLS", server: `{listen: ":8090"}`, admin: `{listen: "0.0.0.0:8091"}`, wantMessage: "not a loopback address"},
		{name: "public without TLS behind a proxy", server: `{listen: ":8090", trusted_proxy: true}`, admin: `{listen: ":8091"}`, wantMessage: "not a loopback address"},
		{name: "public with TLS", server: `{listen: ":8443", tls: {cert_file: c.pem, key_file: k.pem}}`, admin: `{listen: ":8091"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			yaml := strings.Replace(validYAML, `server: {listen: ":8090"}`, "server: "+tt.server, 1)
			if tt.admin != "" {
				yaml += "admin: " + tt.admin + "\n"
			}
			c, err := Parse([]byte(yaml), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
			if err != nil {
				t.Fatal(err)
			}
			var got []Issue
			for _, i := range c.Validate() {
				if i.Key == "admin.listen" {
					got = append(got, i)
				}
			}
			switch {
			case tt.wantMessage == "" && len(got) != 0:
				t.Errorf("issues = %v, want none", got)
			case tt.wantMessage != "" && (len(got) != 1 || got[0].Warning || !strings.Contains(got[0].Message, tt.wantMessage)):
				t.Errorf("issues = %v, want one error containing %q", got, tt.wantMessage)
			}
		})
	}
	// server.insecure_http allows a plain non-loopback admin listener.
	yaml := strings.Replace(validYAML, `server: {listen: ":8090"}`, `server: {listen: ":8090", insecure_http: true}`, 1) +
		"admin: {listen: \":8091\"}\n"
	c, err := Parse([]byte(yaml), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range c.Validate() {
		if i.Key == "admin.listen" {
			t.Errorf("insecure_http: %v", i)
		}
	}
}

func TestCheckApplication(t *testing.T) {
	t.Parallel()
	c := validConfig(t)
	a := Application{ID: "dyn", Rate: Rate{RequestsPerMinute: 5}}
	if issues := c.CheckApplication(&a); issues.Err(false) != nil {
		t.Fatalf("issues = %v", issues)
	}
	if a.Quota.Limit != 1000 || a.Quota.Window != "P1D" || a.Rate.RequestsPerMinute != 5 || a.MaxAttempts != c.Limits.MaxAttempts {
		t.Errorf("defaults not applied: %+v", a)
	}
	bad := Application{ID: "dyn", Quota: Quota{Window: "P1M"}, Documents: AppDocuments{HTML: "pdf"}}
	issues := c.CheckApplication(&bad)
	if len(issues) != 2 || issues[0].Key != "quota.window" || issues[1].Key != "documents.html" {
		t.Errorf("issues = %v, want quota.window and documents.html", issues)
	}
}

func TestServiceValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		yaml        string
		env         map[string]string
		wantKey     string // "" means no issue
		wantMessage string
		wantWarning bool
	}{
		{name: "token set", yaml: "metrics: {token_env: VDE_METRICS_TOKEN}", env: map[string]string{"VDE_METRICS_TOKEN": "s3cret-metrics"}},
		{name: "token variable unset", yaml: "metrics: {token_env: VDE_METRICS_TOKEN}",
			wantKey: "metrics.token_env", wantMessage: "VDE_METRICS_TOKEN is not set"},
		{name: "token variable name invalid", yaml: "metrics: {token_env: \"1BAD\"}",
			wantKey: "metrics.token_env", wantMessage: "not an environment variable name"},
		{name: "loopback listener", yaml: "metrics: {listen: \"127.0.0.1:9090\"}"},
		{name: "listener not host:port", yaml: "metrics: {listen: \"9090\"}", wantKey: "metrics.listen", wantMessage: "host:port"},
		{name: "listener equals server.listen", yaml: "metrics: {listen: \":8090\"}", wantKey: "metrics.listen", wantMessage: "differ"},
		{name: "public listener without token warns", yaml: "metrics: {listen: \"0.0.0.0:9090\"}",
			wantKey: "metrics.listen", wantMessage: "not a loopback address", wantWarning: true},
		{name: "public listener with token", yaml: "metrics: {listen: \"0.0.0.0:9090\", token_env: T}", env: map[string]string{"T": "x"}},
		{name: "worker size zero", yaml: "worker: {size: 0}", wantKey: "worker.size", wantMessage: "between 1 and 256"},
		{name: "worker size too large", yaml: "worker: {size: 1000}", wantKey: "worker.size"},
		{name: "poll interval too short", yaml: "worker: {poll_interval: 1ms}", wantKey: "worker.poll_interval"},
		{name: "sweep interval too short", yaml: "retention: {sweep_interval: 10ms}", wantKey: "retention.sweep_interval"},
		{name: "audit retention too short", yaml: "audit: {retention: 1h}", wantKey: "audit.retention"},
		{name: "audit log on", yaml: "audit: {log: true, retention: 2160h}"},
		{name: "custom values", yaml: "worker: {size: 8, poll_interval: 2s}\nretention: {sweep_interval: 5m}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vars := map[string]string{"ANTHROPIC_API_KEY": testSecret}
			for k, v := range tt.env {
				vars[k] = v
			}
			c, err := Parse([]byte(validYAML+tt.yaml+"\n"), env(vars))
			if err != nil {
				t.Fatal(err)
			}
			issues := c.Validate()
			if tt.wantKey == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %v, want none", issues)
				}
				return
			}
			if len(issues) != 1 {
				t.Fatalf("issues = %v, want exactly one for %s", issues, tt.wantKey)
			}
			i := issues[0]
			if i.Key != tt.wantKey || !strings.Contains(i.Message, tt.wantMessage) || i.Warning != tt.wantWarning {
				t.Errorf("issue = %+v, want key %s containing %q (warning %t)", i, tt.wantKey, tt.wantMessage, tt.wantWarning)
			}
		})
	}
}

func TestMetricsTokenNeverPrinted(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte(validYAML+"metrics: {token_env: T}\n"),
		env(map[string]string{"ANTHROPIC_API_KEY": testSecret, "T": "metrics-secret-value"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Metrics.Token() != "metrics-secret-value" {
		t.Fatalf("Token() = %q, want the variable's value", c.Metrics.Token())
	}
	if strings.Contains(c.String(), "metrics-secret-value") {
		t.Error("Config.String prints the metrics token")
	}
}

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDeliveryDefaults(t *testing.T) {
	t.Parallel()
	c := validConfig(t)
	if c.Sync.MaxWait != 60*time.Second || c.Documents.Fetch.Enabled || c.Documents.Fetch.Timeout != 0 {
		t.Errorf("sync %+v, fetch %+v", c.Sync, c.Documents.Fetch)
	}
	if issues := c.Validate(); issues.Err(true) != nil {
		t.Errorf("defaults: %v", issues)
	}
}

func TestDeliveryValidate(t *testing.T) {
	t.Parallel()
	const secret = "webhook-secret-of-some-length"
	tests := []struct {
		name, yaml string
		key        string // the issue's key; "" for none
		warning    bool
	}{
		{"https webhook", `{id: a, webhook: {url: "https://hooks.example.com/vde", secret_env: S}}`, "", false},
		{"loopback http", `{id: a, webhook: {url: "http://127.0.0.1:9000/hook", secret_env: S}}`, "", false},
		{"remote http", `{id: a, webhook: {url: "http://hooks.example.com/vde", secret_env: S}}`, "applications[0].webhook.url", false},
		{"credentials", `{id: a, webhook: {url: "https://u:url-password@hooks.example.com/", secret_env: S}}`, "applications[0].webhook.url", false},
		{"credentials in an unparseable URL", `{id: a, webhook: {url: "https://u:url-password@hooks example.com/%zz", secret_env: S}}`, "applications[0].webhook.url", false},
		{"no secret_env", `{id: a, webhook: {url: "https://hooks.example.com/"}}`, "applications[0].webhook.secret_env", false},
		{"secret unset", `{id: a, webhook: {url: "https://hooks.example.com/", secret_env: UNSET}}`, "applications[0].webhook.secret_env", false},
		{"short secret", `{id: a, webhook: {url: "https://hooks.example.com/", secret_env: SHORT}}`, "applications[0].webhook.secret_env", true},
		{"unknown event", `{id: a, webhook: {url: "https://hooks.example.com/", secret_env: S, events: [job.created]}}`, "applications[0].webhook.events", false},
		{"secret without url", `{id: a, webhook: {secret_env: S}}`, "applications[0].webhook.url", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			text := strings.Replace(validYAML, "  - {id: travelinbox}", "  - "+tt.yaml, 1)
			c, err := Parse([]byte(text), env(map[string]string{"ANTHROPIC_API_KEY": testSecret, "S": secret, "SHORT": "short"}))
			if err != nil {
				t.Fatal(err)
			}
			var got []Issue
			for _, i := range c.Validate() {
				if strings.Contains(i.Key, "webhook") {
					got = append(got, i)
				}
			}
			switch {
			case tt.key == "" && len(got) > 0:
				t.Errorf("issues %v, want none", got)
			case tt.key != "" && (len(got) != 1 || got[0].Key != tt.key || got[0].Warning != tt.warning):
				t.Errorf("issues %v, want one on %s (warning %v)", got, tt.key, tt.warning)
			}
			if strings.Contains(c.String(), secret) {
				t.Error("Config.String prints the webhook secret")
			}
			if strings.Contains(fmt.Sprint(got), "url-password") {
				t.Errorf("an issue echoes the URL's credentials: %v", got)
			}
			if tt.key == "" && c.Applications[0].Webhook.Secret() != secret {
				t.Error("secret not resolved")
			}
		})
	}
}

func TestSyncAndFetchValidate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, yaml, key string
		warning         bool
	}{
		{"max_wait over the cap", "sync: {max_wait: 3m}", "sync.max_wait", false},
		{"fetch without hosts", "documents: {fetch: {enabled: true}}", "documents.fetch.allowed_hosts", false},
		{"fetch timeout too long", "documents: {fetch: {enabled: true, allowed_hosts: [a.example], timeout: 5m}}", "documents.fetch", false},
		{"fetch timeout negative", "documents: {fetch: {timeout: -1s}}", "documents.fetch", false},
		{"fetch is deprecated", "documents: {fetch: {enabled: true, allowed_hosts: [a.example], max_bytes: 1024, timeout: 10s}}",
			"documents.fetch", true},
		{"fetch timeout alone is deprecated", "documents: {fetch: {timeout: 10s}}", "documents.fetch", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Parse([]byte(validYAML+tt.yaml+"\n"), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, i := range c.Validate() {
				found = found || i.Key == tt.key && i.Warning == tt.warning
			}
			if !found {
				t.Errorf("no issue (warning %t) on %s: %v", tt.warning, tt.key, c.Validate())
			}
		})
	}
}

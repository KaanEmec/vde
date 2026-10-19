package config

import (
	"fmt"
	"net/url"
	"time"
)

// Sync bounds POST /v1/extract (v0.3 epic 05), which holds the request open until the
// job is terminal.
type Sync struct {
	MaxWait time.Duration `yaml:"max_wait"` // longest wait before answering 202; at most MaxSyncWait
}

// Fetch lets a JSON submission name its document by URL (documents.fetch). Off by
// default; only HTTPS URLs on AllowedHosts are fetched, and never from a loopback,
// private or link-local address.
type Fetch struct {
	Enabled      bool          `yaml:"enabled"`
	AllowedHosts []string      `yaml:"allowed_hosts,omitempty"` // exact host names, no wildcards
	MaxBytes     int64         `yaml:"max_bytes"`               // 0 means limits.document_bytes
	Timeout      time.Duration `yaml:"timeout"`                 // the whole fetch
}

// Webhook is an application's completion webhook (applications[].webhook): the operator
// approves the URL, the service signs each delivery with the secret.
type Webhook struct {
	URL       string   `yaml:"url,omitempty"`
	SecretEnv string   `yaml:"secret_env,omitempty"` // names the environment variable holding the secret
	Events    []string `yaml:"events,omitempty"`     // job.finished; the default

	secret string // resolved from SecretEnv at load; never serialized or printed
}

// Secret returns the signing secret resolved from SecretEnv, or "".
func (w Webhook) Secret() string { return w.secret }

// WebhookJobFinished is the only webhook event.
const WebhookJobFinished = "job.finished"

const (
	defaultSyncMaxWait = 60 * time.Second
	// MaxSyncWait caps sync.max_wait.
	MaxSyncWait         = 120 * time.Second
	defaultFetchTimeout = 10 * time.Second
	minWebhookSecret    = 16
)

// resolveDelivery applies the delivery defaults and resolves the webhook secrets.
func (c *Config) resolveDelivery(getenv func(string) string) {
	setDefault(&c.Sync.MaxWait, defaultSyncMaxWait)
	setDefault(&c.Documents.Fetch.Timeout, defaultFetchTimeout)
	for i := range c.Applications {
		w := &c.Applications[i].Webhook
		if w.URL == "" {
			continue
		}
		if len(w.Events) == 0 {
			w.Events = []string{WebhookJobFinished}
		}
		if w.SecretEnv != "" {
			w.secret = getenv(w.SecretEnv)
		}
	}
}

// delivery checks sync, documents.fetch and applications[].webhook. Only variable
// names are printed, never a secret.
func (v *validator) delivery(c *Config) {
	if c.Sync.MaxWait <= 0 || c.Sync.MaxWait > MaxSyncWait {
		v.errorf("sync.max_wait", "%s must be positive and at most %s", c.Sync.MaxWait, MaxSyncWait)
	}
	f := c.Documents.Fetch
	if f.Enabled && len(f.AllowedHosts) == 0 {
		v.errorf("documents.fetch.allowed_hosts", "at least one host is required when fetch is enabled")
	}
	if f.MaxBytes < 0 || f.Timeout <= 0 || f.Timeout > time.Minute {
		v.errorf("documents.fetch", "max_bytes must not be negative and timeout must be positive and at most 1m")
	}
	for i, a := range c.Applications {
		w := a.Webhook
		key := fmt.Sprintf("applications[%d].webhook", i)
		if w.URL == "" {
			if w.SecretEnv != "" || len(w.Events) > 0 {
				v.errorf(key+".url", "is required when a webhook is configured")
			}
			continue
		}
		u, err := url.Parse(w.URL)
		switch {
		case err != nil: // the text is not quoted: it may carry credentials
			v.errorf(key+".url", "is not a valid URL")
		case u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http"):
			v.errorf(key+".url", "%q is not an absolute http or https URL without credentials", u.Redacted())
		case u.Scheme == "http" && !isLoopback(u.Hostname()) && !c.Server.InsecureHTTP:
			v.errorf(key+".url", "must be https unless it is a loopback address or server.insecure_http is true")
		}
		switch {
		case w.SecretEnv == "":
			v.errorf(key+".secret_env", "is required")
		case !envNameRe.MatchString(w.SecretEnv):
			v.errorf(key+".secret_env", "%q is not an environment variable name", w.SecretEnv)
		case w.secret == "":
			v.errorf(key+".secret_env", "environment variable %s is not set", w.SecretEnv)
		case len(w.secret) < minWebhookSecret:
			v.warnf(key+".secret_env", "the secret in %s is shorter than %d bytes", w.SecretEnv, minWebhookSecret)
		}
		for _, e := range w.Events {
			v.oneOf(key+".events", e, []string{WebhookJobFinished})
		}
	}
}

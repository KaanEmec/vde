package config

import (
	"net"
	"time"
)

// Metrics protects GET /metrics (contract §12: by network or a separate token).
type Metrics struct {
	// TokenEnv names the environment variable holding a bearer token that /metrics
	// requires. Empty means no token.
	TokenEnv string `yaml:"token_env"`
	// Listen is a second listener (host:port) serving only /metrics. Empty serves it on
	// server.listen, to loopback peers only unless TokenEnv is set.
	Listen string `yaml:"listen"`
	// GoRuntime adds the Go runtime and process collectors.
	GoRuntime bool `yaml:"go_runtime"`

	token string // resolved from TokenEnv at load; never serialized or printed
}

// Token returns the metrics token resolved from TokenEnv, or "".
func (m Metrics) Token() string { return m.token }

// Admin configures the admin API (epic 11): /admin/v1 on its own listener, reached with
// admin tokens (vde token create --admin).
type Admin struct {
	// Listen is the admin listener (host:port); empty disables the admin API. A
	// non-loopback address needs server.tls (the admin listener serves the same
	// certificate) or server.insecure_http.
	Listen string `yaml:"listen"`
}

// Worker sizes the job worker pool of serve.
type Worker struct {
	Size int `yaml:"size"` // concurrent jobs
	// PollInterval is the sleep between empty leases. Across processes (vde serve --role
	// api and worker replicas) it is the wake-up: an API process cannot wake another
	// process's workers, so a job waits at most this long before a worker looks.
	PollInterval time.Duration `yaml:"poll_interval"`
	// Lease is how long a worker's lease on a running job lasts without renewal. The
	// worker renews it every Lease/3 and at each attempt; a job whose worker died is
	// leased again by any process once it passed.
	Lease time.Duration `yaml:"lease"`
}

// ServiceRate is the process-wide rate limiting choice; the limits themselves are
// applications[].rate.
type ServiceRate struct {
	// Shared counts requests_per_minute and submits_per_minute in fixed one-minute
	// windows in the job store, so that every replica counts against one limit (one
	// UPSERT per request). False (the default) keeps per-process token buckets, which
	// admit up to the limit per replica.
	Shared bool `yaml:"shared"`
}

// ServiceRetention holds the process-wide retention settings; per-application
// retention is applications[].retention.
type ServiceRetention struct {
	SweepInterval time.Duration `yaml:"sweep_interval"` // between expiry sweeps
}

// Audit configures the audit trail (the audit_events table).
type Audit struct {
	Log       bool          `yaml:"log"`       // also write each event as a log line with audit=true
	Retention time.Duration `yaml:"retention"` // how long audit events are kept
}

const (
	defaultAdminListen    = "127.0.0.1:8091"
	defaultAuditRetention = 365 * 24 * time.Hour
	minAuditRetention     = 24 * time.Hour
	defaultWorkerSize     = 4
	defaultPollInterval   = 500 * time.Millisecond
	// defaultLease: renewed every 40 s, a lease survives two missed renewals (a database
	// failover, a long pause) before another process may take the job, and a job whose
	// worker died resumes within 2 minutes, well inside the 5-minute job_timeout.
	defaultLease         = 2 * time.Minute
	minLease             = 3 * time.Second
	defaultSweepInterval = time.Minute
	maxWorkerSize        = 256
	minPollInterval      = 10 * time.Millisecond
	minSweepInterval     = time.Second
)

// service checks the metrics, admin, worker, retention and audit keys. Only names are printed, never
// the token.
func (v *validator) service(c *Config) {
	m := c.Metrics
	switch {
	case m.TokenEnv == "":
	case !envNameRe.MatchString(m.TokenEnv):
		v.errorf("metrics.token_env", "%q is not an environment variable name", m.TokenEnv)
	case m.token == "":
		v.errorf("metrics.token_env", "environment variable %s is not set", m.TokenEnv)
	}
	if m.Listen != "" {
		host, _, err := net.SplitHostPort(m.Listen)
		switch {
		case err != nil:
			v.errorf("metrics.listen", "%q is not host:port", m.Listen)
		case m.Listen == c.Server.Listen:
			v.errorf("metrics.listen", "must differ from server.listen")
		case !isLoopback(host) && m.TokenEnv == "":
			v.warnf("metrics.listen", "%q is not a loopback address and metrics.token_env is not set: "+
				"anyone who reaches it can read the metrics", m.Listen)
		}
	}
	if a := c.Admin.Listen; a != "" {
		host, _, err := net.SplitHostPort(a)
		switch {
		case err != nil:
			v.errorf("admin.listen", "%q is not host:port", a)
		case a == c.Server.Listen || a == m.Listen:
			v.errorf("admin.listen", "must differ from server.listen and metrics.listen")
		case !isLoopback(host) && c.Server.TLS.CertFile == "" && !c.Server.InsecureHTTP:
			v.errorf("admin.listen", "%q is not a loopback address: set server.tls (the admin listener "+
				"serves the same certificate), or server.insecure_http on a trusted private network", a)
		}
	}
	if c.Worker.Size < 1 || c.Worker.Size > maxWorkerSize {
		v.errorf("worker.size", "%d is not between 1 and %d", c.Worker.Size, maxWorkerSize)
	}
	if c.Worker.PollInterval < minPollInterval {
		v.errorf("worker.poll_interval", "must be at least %s", minPollInterval)
	}
	if c.Worker.Lease < minLease {
		v.errorf("worker.lease", "must be at least %s", minLease)
	}
	if c.Rate.Shared && c.Storage.Driver != StoragePostgres {
		v.warnf("rate.shared", "with storage.driver %s only one process uses the database: the shared "+
			"counters work but per-process limits are already exact", c.Storage.Driver)
	}
	if c.Retention.SweepInterval < minSweepInterval {
		v.errorf("retention.sweep_interval", "must be at least %s", minSweepInterval)
	}
	if c.Audit.Retention < minAuditRetention {
		v.errorf("audit.retention", "must be at least %s", minAuditRetention)
	}
}

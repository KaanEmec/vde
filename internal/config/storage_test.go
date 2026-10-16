package config

import (
	"fmt"
	"strings"
	"testing"
)

const testDSN = "postgres://vde:pg-secret-password@db.internal:5432/vde"

func TestStorageDefaults(t *testing.T) {
	t.Parallel()
	c := validConfig(t)
	if c.Storage.Driver != StorageSQLite || c.Storage.DSNEnv != "" || c.Storage.DSN() != "" {
		t.Errorf("storage = %+v, want sqlite with no DSN", c.Storage)
	}
	if c.Blobs != (Blobs{Driver: BlobsFS, SSE: SSEAES256}) {
		t.Errorf("blobs = %+v, want fs with SSE AES256", c.Blobs)
	}
	cfg, err := Parse([]byte(validYAML+"storage: {driver: postgres}\n"), env(map[string]string{
		"ANTHROPIC_API_KEY": testSecret, "VDE_DATABASE_URL": testDSN,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.DSNEnv != "VDE_DATABASE_URL" || cfg.Storage.DSN() != testDSN {
		t.Errorf("postgres storage = %q / %q, want the DSN from VDE_DATABASE_URL", cfg.Storage.DSNEnv, cfg.Storage.DSN())
	}
	if issues := cfg.Validate(); len(issues) != 1 || issues[0].Key != "blobs.driver" || !issues[0].Warning {
		t.Errorf("postgres with fs blobs: %v, want one warning on blobs.driver", issues)
	}
	// An explicit empty sse turns server-side encryption off.
	cfg, err = Parse([]byte(validYAML+"blobs: {driver: s3, bucket: vde-docs, region: eu-central-1, sse: \"\"}\n"),
		env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Blobs.SSE != "" || len(cfg.Validate()) != 0 {
		t.Errorf("blobs = %+v, issues %v", cfg.Blobs, cfg.Validate())
	}
}

func TestStorageValidate(t *testing.T) {
	t.Parallel()
	s3 := func(c *Config) {
		c.Blobs = Blobs{Driver: BlobsS3, Bucket: "vde-docs", Region: "eu-central-1", SSE: SSEAES256}
	}
	pg := func(c *Config) {
		c.Storage = Storage{Driver: StoragePostgres, DSNEnv: "VDE_DATABASE_URL", dsn: testDSN}
		s3(c)
	}
	tests := []struct {
		name        string
		edit        func(c *Config)
		wantKey     string // "" means no issue at all
		wantMessage string
		wantWarning bool
	}{
		{name: "postgres and s3", edit: pg},
		{name: "minio endpoint", edit: func(c *Config) {
			s3(c)
			c.Blobs.Endpoint, c.Blobs.PathStyle, c.Blobs.SSE = "http://127.0.0.1:9000", true, ""
		}},
		{name: "unknown storage driver", edit: func(c *Config) { c.Storage.Driver = "mysql" },
			wantKey: "storage.driver", wantMessage: `"mysql" is not sqlite or postgres`},
		{name: "dsn variable unset", edit: func(c *Config) { pg(c); c.Storage.dsn = "" },
			wantKey: "storage.dsn_env", wantMessage: "VDE_DATABASE_URL is not set"},
		{name: "bad dsn variable name", edit: func(c *Config) { pg(c); c.Storage.DSNEnv = "not a name" },
			wantKey: "storage.dsn_env", wantMessage: "is not an environment variable name"},
		{name: "dsn is not a connection string", edit: func(c *Config) { pg(c); c.Storage.dsn = "mysql:x" },
			wantKey: "storage.dsn_env", wantMessage: "neither a postgres:// URL"},
		{name: "key=value dsn", edit: func(c *Config) { pg(c); c.Storage.dsn = "host=db user=vde password=x" }},
		{name: "negative pool", edit: func(c *Config) { pg(c); c.Storage.MaxOpenConns = -1 },
			wantKey: "storage.max_open_conns", wantMessage: "is not between 0 and 1000"},
		{name: "pool smaller than the workers", edit: func(c *Config) { pg(c); c.Storage.MaxOpenConns = 2 },
			wantKey: "storage.max_open_conns", wantMessage: "requests will wait", wantWarning: true},
		{name: "sqlite with a dsn", edit: func(c *Config) { c.Storage.DSNEnv = "VDE_DATABASE_URL" },
			wantKey: "storage", wantMessage: "ignored with driver sqlite", wantWarning: true},
		{name: "unknown blob driver", edit: func(c *Config) { c.Blobs.Driver = "gcs" },
			wantKey: "blobs.driver", wantMessage: `"gcs" is not fs or s3`},
		{name: "fs with s3 keys", edit: func(c *Config) { c.Blobs.Bucket = "vde-docs" },
			wantKey: "blobs", wantMessage: "ignored with driver fs", wantWarning: true},
		{name: "s3 without bucket", edit: func(c *Config) { s3(c); c.Blobs.Bucket = "" },
			wantKey: "blobs.bucket", wantMessage: "is not an S3 bucket name"},
		{name: "s3 bucket with capitals", edit: func(c *Config) { s3(c); c.Blobs.Bucket = "VDE" },
			wantKey: "blobs.bucket", wantMessage: "is not an S3 bucket name"},
		{name: "s3 without region", edit: func(c *Config) { s3(c); c.Blobs.Region = "" },
			wantKey: "blobs.region", wantMessage: "is required"},
		{name: "endpoint with a path", edit: func(c *Config) { s3(c); c.Blobs.Endpoint = "https://s3.example.com/bucket" },
			wantKey: "blobs.endpoint", wantMessage: "is not an http(s) URL without a path"},
		{name: "plain remote endpoint", edit: func(c *Config) { s3(c); c.Blobs.Endpoint = "http://minio.internal:9000" },
			wantKey: "blobs.endpoint", wantMessage: "plain HTTP", wantWarning: true},
		{name: "unknown sse", edit: func(c *Config) { s3(c); c.Blobs.SSE = "aws:kms" },
			wantKey: "blobs.sse", wantMessage: `"aws:kms" is not AES256 or empty`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := validConfig(t)
			tt.edit(c)
			issues := c.Validate()
			if tt.wantKey == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %v, want none", issues)
				}
				return
			}
			if len(issues) != 1 {
				t.Fatalf("issues = %v, want exactly one on %s", issues, tt.wantKey)
			}
			i := issues[0]
			if i.Key != tt.wantKey || !strings.Contains(i.Message, tt.wantMessage) || i.Warning != tt.wantWarning {
				t.Errorf("issue = %+v, want key %s, message containing %q, warning %v", i, tt.wantKey, tt.wantMessage, tt.wantWarning)
			}
			if strings.Contains(i.Message, "pg-secret-password") {
				t.Errorf("issue %q prints the DSN", i.Message)
			}
		})
	}
}

func TestDSNNeverPrinted(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(validYAML+"storage: {driver: postgres}\n"), env(map[string]string{
		"ANTHROPIC_API_KEY": testSecret, "VDE_DATABASE_URL": testDSN,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{cfg.String(), fmt.Sprintf("%v %+v %#v", cfg, cfg.Storage, cfg)} {
		if strings.Contains(out, "pg-secret-password") {
			t.Errorf("the DSN's password is printed:\n%s", out)
		}
	}
}

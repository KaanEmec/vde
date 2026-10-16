package config

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Storage chooses the job store: SQLite in server.data_dir (one process) or PostgreSQL
// (several replicas sharing one queue).
type Storage struct {
	Driver string `yaml:"driver"` // sqlite (default) or postgres
	// DSNEnv names the environment variable holding the PostgreSQL connection string
	// (postgres://… or key=value); VDE_DATABASE_URL when unset. The DSN carries the
	// password, so it is never in the file and never printed.
	DSNEnv string `yaml:"dsn_env"`
	// MaxOpenConns caps the PostgreSQL connection pool; 0 means 16.
	MaxOpenConns int `yaml:"max_open_conns"`

	dsn string // resolved from DSNEnv at load; never serialized or printed
}

// DSN returns the PostgreSQL connection string resolved from DSNEnv, or "".
func (s Storage) DSN() string { return s.dsn }

// String describes the storage with the DSN redacted.
func (s Storage) String() string {
	dsn := "unset"
	if s.dsn != "" {
		dsn = "[redacted]"
	}
	return "driver=" + s.Driver + " dsn_env=" + s.DSNEnv + " dsn=" + dsn + " max_open_conns=" + strconv.Itoa(s.MaxOpenConns)
}

// GoString keeps %#v from printing the DSN.
func (s Storage) GoString() string { return "config.Storage{" + s.String() + "}" }

// Blobs chooses the blob store of documents and page images: files under
// server.data_dir/blobs, or an S3-compatible bucket shared by every replica.
// Credentials of s3 come from the AWS default chain (AWS_ACCESS_KEY_ID and
// AWS_SECRET_ACCESS_KEY, a shared profile, or the instance or task role), never from
// the file.
type Blobs struct {
	Driver string `yaml:"driver"` // fs (default) or s3
	Bucket string `yaml:"bucket"`
	// Endpoint is the S3 API URL of a non-AWS store (MinIO, R2, Backblaze); empty means
	// AWS S3 in Region.
	Endpoint string `yaml:"endpoint"`
	// Region is where the bucket lives (auto for R2, any value for MinIO). It decides
	// where documents are stored: see the privacy document.
	Region    string `yaml:"region"`
	PathStyle bool   `yaml:"path_style"` // bucket in the path, not the host name (MinIO)
	SSE       string `yaml:"sse"`        // server-side encryption: AES256 (default) or "" for none
}

// Storage and blob drivers.
const (
	StorageSQLite   = "sqlite"
	StoragePostgres = "postgres"
	BlobsFS         = "fs"
	BlobsS3         = "s3"
	// SSEAES256 is SSE-S3: the store encrypts each object with keys it manages.
	SSEAES256 = "AES256"

	defaultDSNEnv    = "VDE_DATABASE_URL"
	maxPostgresConns = 1000
)

var bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func defaultStorage() Storage { return Storage{Driver: StorageSQLite} }

func defaultBlobs() Blobs { return Blobs{Driver: BlobsFS, SSE: SSEAES256} }

// resolve applies the postgres default of dsn_env and reads the DSN through getenv.
func (s *Storage) resolve(getenv func(string) string) {
	if s.Driver == StoragePostgres && s.DSNEnv == "" {
		s.DSNEnv = defaultDSNEnv
	}
	if s.DSNEnv != "" {
		s.dsn = getenv(s.DSNEnv)
	}
}

// storage checks the storage and blobs keys. The DSN is never printed.
func (v *validator) storage(c *Config) {
	s := c.Storage
	switch s.Driver {
	case StorageSQLite:
		if s.DSNEnv != "" || s.MaxOpenConns != 0 {
			v.warnf("storage", "dsn_env and max_open_conns are ignored with driver sqlite")
		}
	case StoragePostgres:
		switch {
		case !envNameRe.MatchString(s.DSNEnv):
			v.errorf("storage.dsn_env", "%q is not an environment variable name", s.DSNEnv)
		case s.dsn == "":
			v.errorf("storage.dsn_env", "environment variable %s is not set", s.DSNEnv)
		case !strings.HasPrefix(s.dsn, "postgres://") && !strings.HasPrefix(s.dsn, "postgresql://") && !strings.Contains(s.dsn, "="):
			v.errorf("storage.dsn_env", "environment variable %s is neither a postgres:// URL nor a key=value connection string", s.DSNEnv)
		}
		switch {
		case s.MaxOpenConns < 0 || s.MaxOpenConns > maxPostgresConns:
			v.errorf("storage.max_open_conns", "%d is not between 0 and %d", s.MaxOpenConns, maxPostgresConns)
		case s.MaxOpenConns > 0 && s.MaxOpenConns < c.Worker.Size+2:
			v.warnf("storage.max_open_conns", "%d connections for %d workers plus the API and the sweeper: "+
				"requests will wait for a connection", s.MaxOpenConns, c.Worker.Size)
		}
	default:
		v.errorf("storage.driver", "%q is not sqlite or postgres", s.Driver)
	}

	b := c.Blobs
	switch b.Driver {
	case BlobsFS:
		if b.Bucket != "" || b.Endpoint != "" || b.Region != "" || b.PathStyle {
			v.warnf("blobs", "bucket, endpoint, region and path_style are ignored with driver fs")
		}
		if s.Driver == StoragePostgres {
			v.warnf("blobs.driver", "fs with storage.driver postgres: replicas on other hosts cannot read "+
				"each other's documents; use s3 unless every replica shares server.data_dir")
		}
	case BlobsS3:
		if !bucketRe.MatchString(b.Bucket) || strings.Contains(b.Bucket, "..") {
			v.errorf("blobs.bucket", "%q is not an S3 bucket name (3 to 63 lowercase letters, digits, dots and hyphens)", b.Bucket)
		}
		if b.Region == "" {
			v.errorf("blobs.region", "is required with driver s3 (it decides where documents are stored)")
		}
		if b.Endpoint != "" {
			u, err := url.Parse(b.Endpoint)
			switch {
			case err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.Trim(u.Path, "/") != "":
				v.errorf("blobs.endpoint", "%q is not an http(s) URL without a path", b.Endpoint)
			case u.Scheme == "http" && !isLoopback(u.Hostname()):
				v.warnf("blobs.endpoint", "%q is plain HTTP on a non-loopback host: documents cross the network unencrypted", b.Endpoint)
			}
		}
		if !slices.Contains([]string{"", SSEAES256}, b.SSE) {
			v.errorf("blobs.sse", "%q is not AES256 or empty", b.SSE)
		}
	default:
		v.errorf("blobs.driver", "%q is not fs or s3", b.Driver)
	}
}

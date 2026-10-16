package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// closingStore is an open job store of either driver.
type closingStore interface {
	job.Store
	Close() error
	SchemaVersion(ctx context.Context) (int, error)
}

// openStore opens the job store of storage.driver and applies pending migrations:
// SQLite at server.data_dir/vde.db (WAL mode lets a command write while serve runs) or
// PostgreSQL at the DSN in storage.dsn_env. Every command that needs the store (serve,
// app, token, audit, migrate) goes through it. Errors never carry the DSN's password.
func openStore(ctx context.Context, cfg *config.Config) (closingStore, error) {
	switch s := cfg.Storage; s.Driver {
	case config.StoragePostgres:
		if s.DSN() == "" {
			return nil, fmt.Errorf("storage.dsn_env: environment variable %s is not set", s.DSNEnv)
		}
		pg, err := job.OpenPostgres(ctx, s.DSN(), s.MaxOpenConns)
		if err != nil {
			return nil, err // not a typed nil in the interface
		}
		return pg, nil
	case config.StorageSQLite, "":
		db, err := job.Open(cfg.Server.DatabasePath())
		if err != nil {
			return nil, err
		}
		return db, nil
	default:
		return nil, fmt.Errorf("storage.driver: %q is not sqlite or postgres", s.Driver)
	}
}

// openBlobs opens the blob store of blobs.driver: files under server.data_dir/blobs, or
// the S3 bucket (checked reachable with the credentials of the AWS default chain).
func openBlobs(ctx context.Context, cfg *config.Config) (blob.Store, error) {
	switch b := cfg.Blobs; b.Driver {
	case config.BlobsS3:
		s, err := blob.NewS3(ctx, blob.S3Options{
			Bucket: b.Bucket, Endpoint: b.Endpoint, Region: b.Region, PathStyle: b.PathStyle, SSE: b.SSE,
		})
		if err != nil {
			return nil, err
		}
		return s, nil
	case config.BlobsFS, "":
		s, err := blob.NewFS(filepath.Join(cfg.Server.DataDir, "blobs"))
		if err != nil {
			return nil, err
		}
		return s, nil
	default:
		return nil, errors.New("blobs.driver: " + b.Driver + " is not fs or s3")
	}
}

// storageAttrs describes the storage for the start log line: drivers, bucket and
// region, never the DSN.
func storageAttrs(cfg *config.Config) []any {
	attrs := []any{"storage", cfg.Storage.Driver, "blobs", cfg.Blobs.Driver}
	if cfg.Blobs.Driver == config.BlobsS3 {
		attrs = append(attrs, "bucket", cfg.Blobs.Bucket, "region", cfg.Blobs.Region)
	}
	return attrs
}

// storageSummary is the storage line of vde config check: drivers and where the data
// lives, the DSN's variable name but never its value.
func storageSummary(cfg *config.Config) string {
	line := "storage " + cfg.Storage.Driver
	if cfg.Storage.Driver == config.StoragePostgres {
		line += " (DSN from " + cfg.Storage.DSNEnv + ")"
	} else {
		line += " (" + cfg.Server.DatabasePath() + ")"
	}
	line += ", blobs " + cfg.Blobs.Driver
	if cfg.Blobs.Driver == config.BlobsS3 {
		sse := cfg.Blobs.SSE
		if sse == "" {
			sse = "none"
		}
		line += " (bucket " + cfg.Blobs.Bucket + ", region " + cfg.Blobs.Region + ", sse " + sse + ")"
	} else {
		line += " (" + filepath.Join(cfg.Server.DataDir, "blobs") + ")"
	}
	return line
}

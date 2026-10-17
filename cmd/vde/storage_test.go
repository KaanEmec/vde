package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// appendConfig adds YAML to the configuration file at path.
func appendConfig(t *testing.T, path, yaml string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + yaml + "\n"); err != nil {
		t.Fatal(err)
	}
}

var schemaVersionRe = regexp.MustCompile(`^storage (sqlite|postgres): schema version [1-9][0-9]*\n$`)

func TestMigrateSQLite(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := writeServiceConfig(t, dataDir, "app-a")
	for range 2 { // the second run has nothing to apply
		code, out, stderr := cli("migrate", "--config", path)
		if code != exitOK || !schemaVersionRe.MatchString(out) || !strings.HasPrefix(out, "storage sqlite") {
			t.Fatalf("migrate = %d, stdout %q, stderr %q", code, out, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "vde.db")); err != nil {
		t.Errorf("no database after migrate: %v", err)
	}
	if code, _, stderr := cli("migrate", "--config", path, "extra"); code != exitUsage || !strings.Contains(stderr, "unexpected argument") {
		t.Errorf("migrate with an argument = %d, %q", code, stderr)
	}
}

func TestMigratePostgresWithoutDSN(t *testing.T) {
	t.Parallel()
	path := writeServiceConfig(t, t.TempDir(), "app-a")
	appendConfig(t, path, "storage: {driver: postgres, dsn_env: VDE_TEST_DSN_NEVER_SET}")
	code, _, stderr := cli("migrate", "--config", path)
	if code != exitFailure || !strings.Contains(stderr, "VDE_TEST_DSN_NEVER_SET is not set") {
		t.Errorf("migrate without the DSN = %d, %q", code, stderr)
	}
}

func TestOpenPostgresErrorHidesPassword(t *testing.T) {
	t.Setenv("VDE_TEST_BAD_DSN", "postgres://vde:hunter2@127.0.0.1:1/vde?connect_timeout=1")
	path := writeServiceConfig(t, t.TempDir(), "app-a")
	appendConfig(t, path, "storage: {driver: postgres, dsn_env: VDE_TEST_BAD_DSN}")
	code, _, stderr := cli("app", "list", "--config", path)
	if code != exitFailure || strings.Contains(stderr, "hunter2") {
		t.Errorf("app list on an unreachable database = %d, %q; want a failure without the password", code, stderr)
	}
}

// TestServeOnPostgresAndS3 runs migrate, the operator commands and serve with a job on
// PostgreSQL (VDE_TEST_POSTGRES) and an S3-compatible store (VDE_TEST_S3_ENDPOINT,
// credentials from AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY), each in a fresh schema
// and bucket.
func TestServeOnPostgresAndS3(t *testing.T) {
	base, endpoint := os.Getenv("VDE_TEST_POSTGRES"), os.Getenv("VDE_TEST_S3_ENDPOINT")
	if base == "" || endpoint == "" {
		t.Skip("VDE_TEST_POSTGRES and VDE_TEST_S3_ENDPOINT are not both set")
	}
	ctx := t.Context()
	suffix := fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano()%1e9)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	schema := "vde_e2e_" + suffix
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Setenv("VDE_E2E_DATABASE_URL", u.String())

	region := cmp.Or(os.Getenv("VDE_TEST_S3_REGION"), "us-east-1")
	bucket := cmp.Or(os.Getenv("VDE_TEST_S3_BUCKET"), "vde-test") + "-e2e-" + strings.ReplaceAll(suffix, "_", "-")
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint, o.UsePathStyle = aws.String(endpoint), true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	listKeys := func() []string {
		out, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, o := range out.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		return keys
	}
	t.Cleanup(func() {
		for _, k := range listKeys() {
			_, _ = client.DeleteObject(context.Background(), &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(k)})
		}
		_, _ = client.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})

	path := writeServiceConfig(t, t.TempDir(), "app-a")
	appendConfig(t, path, fmt.Sprintf(`storage: {driver: postgres, dsn_env: VDE_E2E_DATABASE_URL, max_open_conns: 8}
blobs: {driver: s3, bucket: %s, endpoint: %q, region: %s, path_style: true, sse: %q}`,
		bucket, endpoint, region, os.Getenv("VDE_TEST_S3_SSE")))

	if code, out, stderr := cli("migrate", "--config", path); code != exitOK || !strings.HasPrefix(out, "storage postgres: schema version") {
		t.Fatalf("migrate = %d, %q, %q", code, out, stderr)
	}
	cfg, issues, err := loadConfigEnv(path, false, os.Getenv)
	if err != nil {
		t.Fatalf("%v: %v", err, issues)
	}
	script, err := fake.Load(filepath.Join(scenarioDir, "accept-first-try.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(sctx, cfg, ln, nil, slog.New(slog.DiscardHandler),
			func(*config.Endpoint) (provider.Provider, error) { return script, nil })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	baseURL := "http://" + ln.Addr().String()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get(baseURL + "/healthz"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not answer")
		}
	}

	code, token, stderr := cli("token", "create", "--config", path, "--app", "app-a", "--scope", "jobs", "--expires", "1d")
	if code != exitOK {
		t.Fatalf("token create = %d: %s", code, stderr)
	}
	token = strings.TrimSpace(token)
	if code, out, _ := cli("app", "list", "--config", path); code != exitOK || !strings.Contains(out, "app-a") {
		t.Errorf("app list = %d, %q", code, out)
	}

	form, contentType := submission(t)
	resp := call(t, http.MethodPost, baseURL+"/v1/jobs", token, form, contentType)
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(resp.body), &created); err != nil || created.ID == "" {
		t.Fatalf("POST /v1/jobs = %d %s", resp.status, resp.body)
	}
	var final struct{ Status, Result json.RawMessage }
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r := call(t, http.MethodGet, baseURL+"/v1/jobs/"+created.ID, token, nil, "")
		if r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &final) != nil {
			t.Fatalf("poll = %d %s", r.status, r.body)
		}
		if s := string(final.Status); s != `"queued"` && s != `"running"` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s", final.Status)
		}
	}
	if string(final.Status) != `"accepted"` || !strings.Contains(string(final.Result), "Jane Example") {
		t.Errorf("terminal job = %s %s", final.Status, final.Result)
	}
	var status string
	if err := admin.QueryRowContext(ctx, `SELECT status FROM `+schema+`.jobs WHERE id = $1`, created.ID).Scan(&status); err != nil || status != "accepted" {
		t.Errorf("job row in PostgreSQL = %q, %v", status, err)
	}
	// retention: none: the document went to S3 and is gone after the terminal state.
	for _, k := range listKeys() {
		if strings.Contains(k, created.ID) {
			t.Errorf("blob %s survives the terminal read", k)
		}
	}
}

package blob

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var s3BucketSeq atomic.Int64

// newS3Test returns a store on a fresh bucket of the S3-compatible store at
// VDE_TEST_S3_ENDPOINT (RustFS in `make storage`), removed with its objects at cleanup, or skips the
// test. Credentials come from AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY;
// VDE_TEST_S3_BUCKET is the bucket name prefix (default vde-test), VDE_TEST_S3_REGION
// the region (default us-east-1) and VDE_TEST_S3_SSE the sse setting (default none:
// MinIO without a KMS refuses SSE-S3).
func newS3Test(t *testing.T) *S3 {
	t.Helper()
	endpoint := os.Getenv("VDE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("VDE_TEST_S3_ENDPOINT is not set")
	}
	prefix := cmp.Or(os.Getenv("VDE_TEST_S3_BUCKET"), "vde-test")
	o := S3Options{
		Bucket:    fmt.Sprintf("%s-%d-%d-%d", prefix, os.Getpid(), time.Now().Unix()%100000, s3BucketSeq.Add(1)),
		Endpoint:  endpoint,
		Region:    cmp.Or(os.Getenv("VDE_TEST_S3_REGION"), "us-east-1"),
		PathStyle: true,
		SSE:       os.Getenv("VDE_TEST_S3_SSE"),
	}
	client, err := newS3Client(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(o.Bucket)}); err != nil {
		t.Fatal(err)
	}
	s, err := NewS3(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(o.Bucket)})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				break
			}
			for _, obj := range page.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(o.Bucket), Key: obj.Key})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(o.Bucket)})
	})
	return s
}

// keys lists every object key of the store's bucket.
func (s *S3) keys(t *testing.T) []string {
	t.Helper()
	var out []string
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket)})
	for p.HasMorePages() {
		page, err := p.NextPage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Contents {
			out = append(out, aws.ToString(o.Key))
		}
	}
	return out
}

func TestS3KeysAndEncryption(t *testing.T) {
	s := newS3Test(t)
	put(t, s, jobA, Document, "doc")
	put(t, s, jobA, Page(1), "p1")
	if got, want := s.keys(t), []string{"jobs/" + jobA + "/document", "jobs/" + jobA + "/page-01"}; !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	head, err := s.client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String("jobs/" + jobA + "/document")})
	if err != nil {
		t.Fatal(err)
	}
	if s.sse != "" && head.ServerSideEncryption != s.sse {
		t.Errorf("server-side encryption = %q, want %q", head.ServerSideEncryption, s.sse)
	}
}

func TestS3SweepOrphans(t *testing.T) {
	s := newS3Test(t)
	for _, id := range []string{jobA, jobB} {
		put(t, s, id, Document, "x")
		put(t, s, id, Page(1), "x")
	}
	keep := func(id string) bool { return id == jobB }
	// Objects written within the grace period are never swept.
	if n, err := s.SweepOrphans(t.Context(), keep); err != nil || n != 0 {
		t.Fatalf("sweep of new objects = %d, %v; want 0", n, err)
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	n, err := s.SweepOrphans(t.Context(), keep)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want 1", n, err)
	}
	if _, err := read(t, s, jobA, Document); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan survived: %v", err)
	}
	if got, _ := read(t, s, jobB, Page(1)); got != "x" {
		t.Error("a kept job was swept")
	}
}

// TestS3SweepBounded: with two-object pages and one page per run, every run makes
// progress, the run that reaches the end starts over, and every orphan goes in the end.
func TestS3SweepBounded(t *testing.T) {
	s := newS3Test(t)
	s.listPage, s.sweepPages = 2, 1
	var ids []string
	for i := range 5 {
		id := fmt.Sprintf("j_01JA3V9Q6ZK7M4W8X2R5T1N0%02d", i)
		ids = append(ids, id)
		put(t, s, id, Document, "x")
		put(t, s, id, Page(1), "x")
		put(t, s, id, Page(2), "x") // three objects: a job spans pages
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	kept := ids[2]
	total := 0
	for run := range 20 {
		n, err := s.SweepOrphans(t.Context(), func(id string) bool { return id == kept })
		if err != nil {
			t.Fatal(err)
		}
		total += n
		if total == len(ids)-1 {
			if run == 0 {
				t.Error("one bounded run swept everything")
			}
			break
		}
	}
	if total != len(ids)-1 {
		t.Fatalf("swept %d jobs in 20 runs, want %d", total, len(ids)-1)
	}
	if got := s.keys(t); len(got) != 3 {
		t.Errorf("keys left = %v, want the kept job's three", got)
	}
}

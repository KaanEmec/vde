package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Options configures NewS3. Credentials come from the AWS default chain (environment,
// shared profile, instance or task role), never from here.
type S3Options struct {
	Bucket    string
	Endpoint  string // S3 API URL of a non-AWS store; empty means AWS S3 in Region
	Region    string
	PathStyle bool   // bucket in the URL path (MinIO and most self-hosted stores)
	SSE       string // server-side encryption header: "AES256" (SSE-S3) or "" for none
}

// S3 keys and bounds.
const (
	s3Prefix = "jobs/" // objects are jobs/<job id>/<name>
	// s3ListPage is the page size of every listing (the S3 maximum).
	s3ListPage = 1000
	// s3SweepPages bounds one SweepOrphans run to this many listing pages (10,000
	// objects, some 500 jobs with pages); the next run continues where it stopped.
	s3SweepPages  = 10
	s3OpenTimeout = 10 * time.Second
)

// S3 stores blobs as objects jobs/<job id>/<name> in one bucket of an S3-compatible
// store (AWS S3, MinIO, Cloudflare R2, Backblaze B2), so every replica reads every
// document. A PutObject is atomic: readers see the whole object or none.
type S3 struct {
	client     *s3.Client
	bucket     string
	sse        types.ServerSideEncryption
	now        func() time.Time
	listPage   int32
	sweepPages int

	// One sweep at a time. A bounded run leaves the key to continue after and the job
	// whose objects it was listing, with the newest time seen so far.
	mu            sync.Mutex
	cursor        string // "" lists from the beginning
	pendingID     string
	pendingNewest time.Time
}

var _ Store = (*S3)(nil)

// NewS3 returns a store on the bucket and checks that it is reachable with the
// credentials of the default chain.
func NewS3(ctx context.Context, o S3Options) (*S3, error) {
	client, err := newS3Client(ctx, o)
	if err != nil {
		return nil, err
	}
	s := &S3{
		client: client, bucket: o.Bucket, sse: types.ServerSideEncryption(o.SSE), now: time.Now,
		listPage: s3ListPage, sweepPages: s3SweepPages,
	}
	hctx, cancel := context.WithTimeout(ctx, s3OpenTimeout)
	defer cancel()
	if _, err := client.HeadBucket(hctx, &s3.HeadBucketInput{Bucket: aws.String(o.Bucket)}); err != nil {
		return nil, fmt.Errorf("blob: bucket %q: %w", o.Bucket, err)
	}
	return s, nil
}

func newS3Client(ctx context.Context, o S3Options) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(o.Region))
	if err != nil {
		return nil, fmt.Errorf("blob: s3 configuration: %w", err)
	}
	return s3.NewFromConfig(cfg, func(so *s3.Options) {
		so.UsePathStyle = o.PathStyle
		if o.Endpoint != "" {
			so.BaseEndpoint = aws.String(o.Endpoint)
			// S3-compatible stores do not all accept the trailing checksums AWS S3 now
			// takes by default; send and verify checksums only where the API needs them.
			so.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			so.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		}
	}), nil
}

func (s *S3) key(jobID, name string) (string, error) {
	if !validJobID.MatchString(jobID) {
		return "", errors.New("blob: invalid job id")
	}
	if !validName.MatchString(name) {
		return "", errors.New("blob: invalid blob name")
	}
	return s3Prefix + jobID + "/" + name, nil
}

// Put implements Store. The blob is read into memory first (documents and pages are
// bounded by limits.document_bytes and limits.page_bytes): S3 needs the length up front,
// and a reader that fails midway then stores nothing.
func (s *S3) Put(ctx context.Context, jobID, name string, r io.Reader) error {
	key, err := s.key(jobID, name)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(readerWithContext{ctx, r})
	if err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentType:   aws.String("application/octet-stream"),
	}
	if s.sse != "" {
		in.ServerSideEncryption = s.sse
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	return nil
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, jobID, name string) (io.ReadCloser, error) {
	key, err := s.key(jobID, name)
	if err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if isS3NotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: get: %w", err)
	}
	return out.Body, nil
}

func isS3NotFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	var re *awshttp.ResponseError
	return errors.As(err, &nsk) || errors.As(err, &nf) ||
		errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound
}

// DeleteAll implements Store: it lists the job's prefix page by page and deletes each
// page's objects in one request.
func (s *S3) DeleteAll(ctx context.Context, jobID string) error {
	if !validJobID.MatchString(jobID) {
		return errors.New("blob: invalid job id")
	}
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket), Prefix: aws.String(s3Prefix + jobID + "/"), MaxKeys: aws.Int32(s.listPage),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("blob: delete: %w", err)
		}
		ids := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, o := range page.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: o.Key})
		}
		if len(ids) == 0 {
			continue
		}
		out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket), Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("blob: delete: %w", err)
		}
		if len(out.Errors) > 0 {
			e := out.Errors[0]
			return fmt.Errorf("blob: delete: %d objects not deleted: %s %s", len(out.Errors), aws.ToString(e.Code), aws.ToString(e.Key))
		}
	}
	return nil
}

// SweepOrphans implements Store. It lists jobs/ in key order, where a job's objects are
// adjacent, and judges each job by its newest object. One run lists at most
// s3SweepPages pages; the next run continues after the last key listed, and a run that
// reaches the end starts the next one from the beginning.
func (s *S3) SweepOrphans(ctx context.Context, keep func(jobID string) bool) (_ int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil { // start the next run from the beginning
			s.cursor, s.pendingID, s.pendingNewest = "", "", time.Time{}
		}
	}()
	in := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(s3Prefix), MaxKeys: aws.Int32(s.listPage)}
	if s.cursor != "" {
		in.StartAfter = aws.String(s.cursor)
	}
	p := s3.NewListObjectsV2Paginator(s.client, in)
	cutoff := s.now().Add(-orphanGrace)
	removed := 0
	cur, newest := s.pendingID, s.pendingNewest // the job whose objects are being listed
	judge := func() error {
		if cur == "" || newest.After(cutoff) || keep(cur) {
			return nil
		}
		if err := s.DeleteAll(ctx, cur); err != nil {
			return err
		}
		removed++
		return nil
	}
	for pages := 0; p.HasMorePages(); pages++ {
		if pages == s.sweepPages {
			s.pendingID, s.pendingNewest = cur, newest
			return removed, nil
		}
		page, err := p.NextPage(ctx)
		if err != nil {
			return removed, fmt.Errorf("blob: sweep: %w", err)
		}
		for _, o := range page.Contents {
			key := aws.ToString(o.Key)
			id, _, ok := strings.Cut(strings.TrimPrefix(key, s3Prefix), "/")
			if ok && validJobID.MatchString(id) { // else not an object this store wrote
				if id != cur {
					if err := judge(); err != nil {
						return removed, err
					}
					cur, newest = id, time.Time{}
				}
				if t := aws.ToTime(o.LastModified); t.After(newest) {
					newest = t
				}
			}
			s.cursor = key
		}
	}
	if err := judge(); err != nil {
		return removed, err
	}
	s.cursor, s.pendingID, s.pendingNewest = "", "", time.Time{}
	return removed, nil
}

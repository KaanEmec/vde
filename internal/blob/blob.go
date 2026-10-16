package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Blob names. Pages are page-01 … page-NN in request order; media types live on the
// job row.
const Document = "document"

// Page returns the blob name of page n, counted from 1.
func Page(n int) string { return fmt.Sprintf("page-%02d", n) }

// ErrNotFound: the blob does not exist.
var ErrNotFound = errors.New("blob: not found")

// Store keeps a job's document and page bytes. It never lists or logs content.
type Store interface {
	// Put writes the blob atomically: readers see the whole blob or none.
	Put(ctx context.Context, jobID, name string, r io.Reader) error
	// Get opens the blob; ErrNotFound when it does not exist.
	Get(ctx context.Context, jobID, name string) (io.ReadCloser, error)
	// DeleteAll removes every blob of the job; deleting nothing is not an error.
	DeleteAll(ctx context.Context, jobID string) error
	// SweepOrphans removes the blobs of every job for which keep returns false and
	// returns how many jobs it removed. Jobs touched within the last hour are skipped,
	// so a submission storing its blobs before its row exists is not swept.
	SweepOrphans(ctx context.Context, keep func(jobID string) bool) (int, error)
}

// orphanGrace protects blobs of a submission in progress from SweepOrphans.
const orphanGrace = time.Hour

var (
	validJobID = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)
	validName  = regexp.MustCompile(`^(document|page-[0-9]{2})$`)
)

// FS stores blobs as files: <root>/<shard>/<job id>/<name>, where shard is the last two
// characters of the job id (the random end of a ULID; the first characters are a
// constant prefix and a timestamp). Directories are 0700, files 0600.
type FS struct {
	root string
	now  func() time.Time
}

var _ Store = (*FS)(nil)

// NewFS returns a store rooted at root (typically <data_dir>/blobs), creating it.
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("blob: create root: %w", err)
	}
	return &FS{root: root, now: time.Now}, nil
}

func (s *FS) jobDir(jobID string) (string, error) {
	if !validJobID.MatchString(jobID) {
		return "", errors.New("blob: invalid job id")
	}
	return filepath.Join(s.root, jobID[len(jobID)-2:], jobID), nil
}

func (s *FS) path(jobID, name string) (string, error) {
	dir, err := s.jobDir(jobID)
	if err != nil {
		return "", err
	}
	if !validName.MatchString(name) {
		return "", errors.New("blob: invalid blob name")
	}
	return filepath.Join(dir, name), nil
}

// Put implements Store: the bytes go to a temporary file in the job's directory, which
// is synced and renamed into place.
func (s *FS) Put(ctx context.Context, jobID, name string, r io.Reader) (err error) {
	path, err := s.path(jobID, name)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+name+"-") // mode 0600
	if err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, readerWithContext{ctx, r}); err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("blob: put: %w", err)
	}
	return nil
}

// readerWithContext stops a copy once ctx is done.
type readerWithContext struct {
	ctx context.Context
	r   io.Reader
}

func (r readerWithContext) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Get implements Store.
func (s *FS) Get(ctx context.Context, jobID, name string) (io.ReadCloser, error) {
	path, err := s.path(jobID, name)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: get: %w", err)
	}
	return f, nil
}

// DeleteAll implements Store. The shard directory is removed too once empty.
func (s *FS) DeleteAll(ctx context.Context, jobID string) error {
	dir, err := s.jobDir(jobID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("blob: delete: %w", err)
	}
	_ = os.Remove(filepath.Dir(dir)) // fails while other jobs share the shard
	return nil
}

// SweepOrphans implements Store.
func (s *FS) SweepOrphans(ctx context.Context, keep func(jobID string) bool) (int, error) {
	shards, err := os.ReadDir(s.root)
	if err != nil {
		return 0, fmt.Errorf("blob: sweep: %w", err)
	}
	cutoff := s.now().Add(-orphanGrace)
	removed := 0
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		jobs, err := os.ReadDir(filepath.Join(s.root, shard.Name()))
		if err != nil {
			return removed, fmt.Errorf("blob: sweep: %w", err)
		}
		for _, j := range jobs {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			id := j.Name()
			if !j.IsDir() || !validJobID.MatchString(id) {
				continue
			}
			info, err := j.Info()
			if err != nil || info.ModTime().After(cutoff) || keep(id) {
				continue
			}
			if err := s.DeleteAll(ctx, id); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

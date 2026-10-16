package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// ContentLogger is the sink for debug.log_content: it records document content,
// instructions, candidates and provider bodies, so it never shares the routine logger's
// destination and has no allow-list. Construct it only when debug.log_content is on
// (and the listener is loopback, which config validation enforces). A nil
// *ContentLogger is valid and discards everything, so callers need no checks.
type ContentLogger struct {
	log *slog.Logger
	c   io.Closer
}

// OpenContentLog opens path (created with mode 0600, appended to) as the content log. It
// refuses an empty path and the standard streams, so content cannot land in the routine
// log stream.
func OpenContentLog(path string) (*ContentLogger, error) {
	if path == "" || path == "-" {
		return nil, errors.New("logging: the content log needs a file path, not a standard stream")
	}
	if isStdStream(path) {
		return nil, fmt.Errorf("logging: the content log must not be %s", path)
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("logging: open content log: %w", err)
	}
	if isStdFile(f) {
		_ = f.Close()
		return nil, fmt.Errorf("logging: the content log must not be a standard stream (%s)", path)
	}
	return NewContentLogger(f), nil
}

// NewContentLogger returns a content logger writing JSON lines to w, which it closes on
// Close when w is an io.Closer. The caller picks a destination that is not stdout.
func NewContentLogger(w io.Writer) *ContentLogger {
	cl := &ContentLogger{log: slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	if c, ok := w.(io.Closer); ok {
		cl.c = c
	}
	return cl
}

// Log records msg with attrs at debug level. Any key is accepted.
func (c *ContentLogger) Log(ctx context.Context, msg string, attrs ...slog.Attr) {
	if c == nil {
		return
	}
	c.log.LogAttrs(ctx, slog.LevelDebug, msg, attrs...)
}

// Close closes the underlying file.
func (c *ContentLogger) Close() error {
	if c == nil || c.c == nil {
		return nil
	}
	return c.c.Close()
}

func isStdStream(path string) bool {
	switch filepath.Clean(path) {
	case "/dev/stdout", "/dev/stderr", "/dev/stdin", "/dev/fd/0", "/dev/fd/1", "/dev/fd/2":
		return true
	}
	return false
}

// isStdFile reports whether f is the same file as stdout or stderr (a redirect or a
// symlink to them).
func isStdFile(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	for _, std := range []*os.File{os.Stdout, os.Stderr} {
		if si, err := std.Stat(); err == nil && os.SameFile(fi, si) {
			return true
		}
	}
	return false
}

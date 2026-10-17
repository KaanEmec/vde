// Package pdf reads a PDF's pages, text layer and page images with pdfium compiled to
// WebAssembly (go-pdfium on wazero), so the build stays cgo-free (ADR 0010).
//
// An Engine owns a small pool of pdfium instances, started on the first Open. Each Doc
// holds one instance from Open to Close, every call runs under the engine's timeout, and
// an instance that times out or fails is killed, never reused. The package never logs and
// its errors never carry document content.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// Defaults for a zero Config field.
const (
	DefaultWorkers = 1
	DefaultTimeout = 30 * time.Second
)

// Hard bounds that no configuration lifts.
const (
	maxWorkers = 16
	// memoryLimitPages caps one instance's WebAssembly memory at 1 GiB (64 KiB pages);
	// the spike measured 230 to 490 MB at peak.
	memoryLimitPages = 16384
	// maxRenderPixels refuses a page whose bitmap would exceed about 160 MB (RGBA). An
	// A4 page at 300 dpi is 8.7 megapixels.
	maxRenderPixels = 40_000_000
	MinDPI          = 36
	MaxDPI          = 600
)

// Errors. Open and the Doc methods return them wrapped; test with errors.Is.
var (
	ErrInvalid      = errors.New("pdf: not a readable PDF")
	ErrEncrypted    = errors.New("pdf: encrypted")
	ErrTooManyPages = errors.New("pdf: too many pages")
	ErrTooLarge     = errors.New("pdf: too large")
	ErrPageTooLarge = errors.New("pdf: page too large to render")
	ErrPageRange    = errors.New("pdf: page out of range")
	ErrTimeout      = errors.New("pdf: timed out")
	// ErrBusy: every instance stayed in use for the whole wait. Nothing is wrong with
	// the document or the engine; try again later.
	ErrBusy          = errors.New("pdf: every pdfium instance is busy")
	ErrClosed        = errors.New("pdf: closed")
	errInternalPanic = errors.New("pdf: internal error")
)

// Config bounds an Engine. Zero values take the defaults; MaxPages and MaxBytes of zero
// mean no limit.
type Config struct {
	Workers  int           // pdfium instances at most, each up to about 500 MB; default 1
	CacheDir string        // compilation cache directory; "" runs uncached
	Timeout  time.Duration // per call, and the wait for a free instance; default 30s
	MaxPages int           // Open refuses a document with more pages (ErrTooManyPages)
	MaxBytes int64         // Open refuses larger input (ErrTooLarge)
}

// Engine opens PDFs on a bounded pool of pdfium instances. It is safe for concurrent use.
// The WebAssembly module is compiled on the first Open, not by New.
type Engine struct {
	cfg      Config
	cacheErr error // why the compilation cache is off, nil when on or not configured

	mu      sync.Mutex
	started bool
	closed  bool
	pool    pdfium.Pool
	cache   wazero.CompilationCache
	initErr error
}

// New validates cfg and returns an engine that starts on first use. A cache directory
// that cannot be created or written is not an error: the engine runs uncached and
// CacheErr says why.
func New(cfg Config) (*Engine, error) {
	if cfg.Workers == 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	switch {
	case cfg.Workers < 1 || cfg.Workers > maxWorkers:
		return nil, fmt.Errorf("pdf: workers %d must be between 1 and %d", cfg.Workers, maxWorkers)
	case cfg.Timeout < 0:
		return nil, fmt.Errorf("pdf: negative timeout %s", cfg.Timeout)
	case cfg.MaxPages < 0 || cfg.MaxBytes < 0:
		return nil, errors.New("pdf: negative limit")
	}
	e := &Engine{cfg: cfg}
	if cfg.CacheDir != "" {
		if err := writableDir(cfg.CacheDir); err != nil {
			e.cacheErr = err
			e.cfg.CacheDir = ""
		}
	}
	return e, nil
}

// writableDir creates dir (0700) and checks that a file can be written in it.
func writableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("pdf: compilation cache: %w", err)
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return fmt.Errorf("pdf: compilation cache: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// CacheErr reports why a configured compilation cache is not used (the engine then
// compiles the module, about 1 s, at every process start), or nil.
func (e *Engine) CacheErr() error { return e.cacheErr }

// Workers is the configured number of instances.
func (e *Engine) Workers() int { return e.cfg.Workers }

// Compiled reports whether wazero compiles WebAssembly to native code on this platform.
// Elsewhere it interprets, about 60 times slower.
func Compiled() bool {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd", "netbsd", "windows":
		return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
	}
	return false
}

// initMu serializes engine starts across the process: wazero's lazily computed version
// (read by the compilation cache) is not safe for concurrent first use.
var initMu sync.Mutex

// start initializes the pool once. An init error is kept and returned by every later
// call.
func (e *Engine) start() (pdfium.Pool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrClosed
	}
	if e.started {
		return e.pool, e.initErr
	}
	e.started = true
	initMu.Lock()
	defer initMu.Unlock()
	rc := wazero.NewRuntimeConfig().
		WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
		WithCloseOnContextDone(true). // Kill interrupts a running call
		WithMemoryLimitPages(memoryLimitPages)
	if e.cfg.CacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(e.cfg.CacheDir)
		if err == nil {
			e.cache = cache
			rc = rc.WithCompilationCache(cache)
		} else {
			e.cacheErr = fmt.Errorf("pdf: compilation cache: %w", err)
		}
	}
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:       0, // no instance before the first Open
		MaxIdle:       e.cfg.Workers,
		MaxTotal:      e.cfg.Workers,
		RuntimeConfig: rc,
		FSConfig:      wazero.NewFSConfig(), // no host filesystem inside the sandbox
		Stdout:        io.Discard,
		Stderr:        io.Discard,
	})
	if err != nil {
		e.initErr = fmt.Errorf("pdf: start pdfium: %w", err)
		return nil, e.initErr
	}
	e.pool = pool
	return pool, nil
}

// Close releases the pool. Open fails with ErrClosed afterwards; Docs still open are
// closed with it.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	var err error
	if e.pool != nil {
		err = e.pool.Close()
	}
	if e.cache != nil {
		err = errors.Join(err, e.cache.Close(context.Background()))
	}
	return err
}

// Doc is an open PDF. It holds one pdfium instance until Close, so callers must close it
// promptly: with Workers 1 every other Open waits. Methods are serialized.
type Doc struct {
	mu      sync.Mutex
	inst    pdfium.Pdfium // nil once closed or killed
	ref     references.FPDF_DOCUMENT
	pages   int
	timeout time.Duration
}

// Open loads data into a free instance, waiting at most the configured timeout for one
// (ErrBusy when none became free), and checks the size and page limits. Errors wrap
// ErrInvalid, ErrEncrypted, ErrTooManyPages, ErrTooLarge, ErrTimeout, ErrBusy or
// ErrClosed.
func (e *Engine) Open(ctx context.Context, data []byte) (*Doc, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalid)
	}
	if e.cfg.MaxBytes > 0 && int64(len(data)) > e.cfg.MaxBytes {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, e.cfg.MaxBytes)
	}
	pool, err := e.start()
	if err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	inst, err := pool.GetInstanceWithContext(wait)
	waited := wait.Err()
	cancel()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case waited != nil:
			return nil, fmt.Errorf("%w: no instance free within %s", ErrBusy, e.cfg.Timeout)
		}
		return nil, fmt.Errorf("pdf: get a pdfium instance: %w", err)
	}
	d := &Doc{inst: inst, timeout: e.cfg.Timeout}
	var docErr error // a refusal of this document, as opposed to a failed call
	err = d.call(ctx, func(inst pdfium.Pdfium) error {
		opened, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
		if err != nil {
			docErr = classify(err)
			return nil
		}
		d.ref = opened.Document
		count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: opened.Document})
		if err != nil {
			docErr = classify(err)
			return nil
		}
		d.pages = count.PageCount
		return nil
	})
	if err != nil {
		return nil, err
	}
	switch {
	case docErr == nil && d.pages < 1:
		docErr = fmt.Errorf("%w: no pages", ErrInvalid)
	case docErr == nil && e.cfg.MaxPages > 0 && d.pages > e.cfg.MaxPages:
		docErr = fmt.Errorf("%w: %d pages, the limit is %d", ErrTooManyPages, d.pages, e.cfg.MaxPages)
	}
	if docErr != nil {
		d.Close()
		return nil, docErr
	}
	return d, nil
}

// classify maps a pdfium open error to ErrEncrypted or ErrInvalid.
func classify(err error) error {
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword), errors.Is(err, pdfiumerrors.ErrSecurity):
		return ErrEncrypted
	default:
		return ErrInvalid
	}
}

// PageCount is the number of pages, at least 1.
func (d *Doc) PageCount() int { return d.pages }

// Text returns the text layer of page (0-based) with line breaks normalized to "\n" and
// control characters other than tab and newline removed. A page without text gives "".
func (d *Doc) Text(ctx context.Context, page int) (string, error) {
	if page < 0 || page >= d.pages {
		return "", fmt.Errorf("%w: %d of %d", ErrPageRange, page, d.pages)
	}
	var text string
	err := d.call(ctx, func(inst pdfium.Pdfium) error {
		res, err := inst.GetPageText(&requests.GetPageText{Page: d.page(page)})
		if err != nil {
			return err
		}
		text = res.Text
		return nil
	})
	if err != nil {
		return "", err
	}
	return cleanText(text), nil
}

// cleanText normalizes pdfium's text: CRLF and CR become LF, and other control
// characters (pdfium emits some for hyphenation and markers) are dropped.
func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || r == 0xfffe || r == 0xffff:
			return -1
		}
		return r
	}, s)
}

// Render draws page (0-based) at dpi on a white background, without interactive form
// fields, and returns it PNG-encoded. The same bytes and dpi always give the same PNG.
func (d *Doc) Render(ctx context.Context, page, dpi int) ([]byte, error) {
	if page < 0 || page >= d.pages {
		return nil, fmt.Errorf("%w: %d of %d", ErrPageRange, page, d.pages)
	}
	if dpi < MinDPI || dpi > MaxDPI {
		return nil, fmt.Errorf("pdf: dpi %d must be between %d and %d", dpi, MinDPI, MaxDPI)
	}
	var out []byte
	var pageErr error
	err := d.call(ctx, func(inst pdfium.Pdfium) error {
		size, err := inst.GetPageSizeInPixels(&requests.GetPageSizeInPixels{Page: d.page(page), DPI: dpi})
		if err != nil {
			return err
		}
		if size.Width <= 0 || size.Height <= 0 || int64(size.Width)*int64(size.Height) > maxRenderPixels {
			pageErr = fmt.Errorf("%w: %dx%d pixels at %d dpi", ErrPageTooLarge, size.Width, size.Height, dpi)
			return nil
		}
		res, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{Page: d.page(page), DPI: dpi})
		if err != nil {
			return err
		}
		// The pixels live in WebAssembly memory until Cleanup: encode first.
		defer res.Cleanup()
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, res.Result.RenderedImage); err != nil {
			return err
		}
		out = buf.Bytes()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, pageErr
}

func (d *Doc) page(i int) requests.Page {
	return requests.Page{ByIndex: &requests.PageByIndex{Document: d.ref, Index: i}}
}

// call runs fn on the Doc's instance under ctx and the per-call timeout. On a timeout,
// an error or a panic the instance is killed (pdfium state may be corrupt) and the Doc
// is unusable afterwards; the pool creates a fresh instance for the next Open.
func (d *Doc) call(ctx context.Context, fn func(pdfium.Pdfium) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inst == nil {
		return ErrClosed
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	inst := d.inst
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// The panic value may quote internals, never content; keep it out anyway.
				done <- errInternalPanic
			}
		}()
		done <- fn(inst)
	}()
	select {
	case err := <-done:
		if err != nil {
			d.kill()
			return fmt.Errorf("pdf: pdfium call failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		// Kill cancels the worker's context, which stops the running WebAssembly
		// (WithCloseOnContextDone); the goroutine above then returns into the buffered
		// channel and exits.
		d.kill()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s", ErrTimeout, d.timeout)
		}
		return ctx.Err()
	}
}

// kill drops the instance without returning it for reuse. Callers hold d.mu.
func (d *Doc) kill() {
	if d.inst != nil {
		_ = d.inst.Kill()
		d.inst = nil
	}
}

// Close frees the document and its instance. It is safe to call more than once.
func (d *Doc) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inst == nil {
		return
	}
	if d.ref != "" {
		_, _ = d.inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: d.ref})
	}
	_ = d.inst.Close()
	d.inst = nil
}

// CacheDir is the compilation cache directory under a data directory
// (server.data_dir/pdfium-cache).
func CacheDir(dataDir string) string { return filepath.Join(dataDir, "pdfium-cache") }

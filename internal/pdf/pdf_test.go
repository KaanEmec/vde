package pdf

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/pdf/pdftest"
	"github.com/klippa-app/go-pdfium"
)

// cacheDir is a compilation cache shared by test runs, so only the first engine of the
// first run compiles the module (about 1 s, or 13 s under -race).
// TestEngine_startTimes measures a cold start in its own directory.
var cacheDir = filepath.Join(os.TempDir(), "vde-pdfium-test-cache")

func newEngine(t testing.TB, cfg Config) *Engine {
	t.Helper()
	if cfg.CacheDir == "" {
		cfg.CacheDir = cacheDir
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func booking(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "samples", "booking.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func open(t testing.TB, e *Engine, data []byte) *Doc {
	t.Helper()
	d, err := e.Open(context.Background(), data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "defaults", cfg: Config{}},
		{name: "16 workers", cfg: Config{Workers: 16}},
		{name: "too many workers", cfg: Config{Workers: 17}, wantErr: true},
		{name: "negative workers", cfg: Config{Workers: -1}, wantErr: true},
		{name: "negative timeout", cfg: Config{Timeout: -time.Second}, wantErr: true},
		{name: "negative pages", cfg: Config{MaxPages: -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%+v) error = %v, want error %v", tt.cfg, err, tt.wantErr)
			}
			if err == nil && e.started {
				t.Error("New started the pool; it must start on the first Open")
			}
		})
	}
}

func TestNew_unwritableCache(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := New(Config{CacheDir: filepath.Join(file, "cache")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.CacheErr() == nil {
		t.Error("CacheErr = nil for a cache dir below a regular file")
	}
	if e.cfg.CacheDir != "" {
		t.Errorf("CacheDir = %q, want uncached", e.cfg.CacheDir)
	}
}

func TestEngine_Open_booking(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{})
	d := open(t, e, booking(t))
	if got := d.PageCount(); got != 1 {
		t.Fatalf("PageCount = %d, want 1", got)
	}
	text, err := d.Text(context.Background(), 0)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	for _, want := range []string{"HV-48213", "Jane Example", "EUR 412.50"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	if strings.Contains(text, "\r") {
		t.Error("text contains a carriage return")
	}
	img, err := d.Render(context.Background(), 0, 150)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if cfg.Width != 1240 || cfg.Height != 1755 {
		t.Errorf("render at 150 dpi = %dx%d, want 1240x1755 (A4, 595x842 pt; pdfium rounds up)", cfg.Width, cfg.Height)
	}
	again, err := d.Render(context.Background(), 0, 150)
	if err != nil {
		t.Fatalf("Render again: %v", err)
	}
	if !bytes.Equal(img, again) {
		t.Error("two renders of the same page differ")
	}
}

func TestDoc_Text_pagesInOrder(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{MaxPages: 20})
	d := open(t, e, pdftest.Build(pdftest.Pages(3, 120)...))
	if d.PageCount() != 3 {
		t.Fatalf("PageCount = %d, want 3", d.PageCount())
	}
	for i := range 3 {
		text, err := d.Text(context.Background(), i)
		if err != nil {
			t.Fatalf("Text(%d): %v", i, err)
		}
		if want := "Page " + string(rune('1'+i)); !strings.HasPrefix(text, want) {
			t.Errorf("Text(%d) starts %.10q, want %q", i, text, want)
		}
	}
	if _, err := d.Text(context.Background(), 3); !errors.Is(err, ErrPageRange) {
		t.Errorf("Text(3) error = %v, want ErrPageRange", err)
	}
	if _, err := d.Render(context.Background(), 0, 2000); err == nil {
		t.Error("Render at 2000 dpi succeeded")
	}
}

func TestDoc_imageOnlyPage(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{})
	d := open(t, e, pdftest.Build(pdftest.Page{Image: true}))
	text, err := d.Text(context.Background(), 0)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if strings.TrimSpace(text) != "" {
		t.Errorf("image-only page has text %q", text)
	}
	if _, err := d.Render(context.Background(), 0, 72); err != nil {
		t.Errorf("Render: %v", err)
	}
}

func TestEngine_Open_errors(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{MaxPages: 20, MaxBytes: 1 << 20})
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "empty", data: nil, want: ErrInvalid},
		{name: "corrupt", data: []byte("%PDF-1.4\nthis is not a PDF body\n%%EOF\n"), want: ErrInvalid},
		{name: "not a PDF", data: []byte("hello"), want: ErrInvalid},
		{name: "encrypted", data: pdftest.Encrypted(pdftest.TextPage("secret")), want: ErrEncrypted},
		{name: "too many pages", data: pdftest.Build(pdftest.Pages(25, 10)...), want: ErrTooManyPages},
		{name: "too large", data: make([]byte, 1<<20+1), want: ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := e.Open(context.Background(), tt.data)
			if err == nil {
				d.Close()
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("Open error = %v, want %v", err, tt.want)
			}
		})
	}
	// The engine still works after refusals.
	open(t, e, booking(t))
}

func TestEngine_Open_serialized(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{Workers: 1})
	data := booking(t)
	var inUse, peak atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			d, err := e.Open(context.Background(), data)
			if err != nil {
				errs <- err
				return
			}
			n := inUse.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			_, err = d.Text(context.Background(), 0)
			inUse.Add(-1)
			d.Close()
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if p := peak.Load(); p != 1 {
		t.Errorf("peak concurrent documents = %d, want 1 with Workers 1", p)
	}
}

// TestEngine_Open_busy: when every instance stays in use for the whole wait, Open fails
// with ErrBusy, not ErrTimeout, so callers can retry instead of failing; a cancelled
// context is reported as itself.
func TestEngine_Open_busy(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{Workers: 1, Timeout: 30 * time.Second})
	held := open(t, e, booking(t))
	e.cfg.Timeout = 100 * time.Millisecond // the wait only; held's calls keep their own timeout
	_, err := e.Open(context.Background(), booking(t))
	if !errors.Is(err, ErrBusy) || errors.Is(err, ErrTimeout) {
		t.Fatalf("Open while busy = %v, want ErrBusy", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Open(ctx, booking(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("Open with a cancelled context = %v, want context.Canceled", err)
	}
	held.Close()
	if d, err := e.Open(context.Background(), booking(t)); err != nil {
		t.Errorf("Open after release: %v", err)
	} else {
		d.Close()
	}
}

func TestDoc_call_timeoutKillsInstance(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{Workers: 1})
	d := open(t, e, booking(t))
	d.timeout = 50 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	err := d.call(context.Background(), func(pdfium.Pdfium) error {
		<-release
		return nil
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("call error = %v, want ErrTimeout", err)
	}
	if _, err := d.Text(context.Background(), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Text after a timeout = %v, want ErrClosed", err)
	}
	// With one worker, the next Open gets a fresh instance.
	d2 := open(t, e, booking(t))
	if _, err := d2.Text(context.Background(), 0); err != nil {
		t.Errorf("Text on a fresh instance: %v", err)
	}
}

func TestDoc_call_panicBecomesError(t *testing.T) {
	t.Parallel()
	e := newEngine(t, Config{Workers: 1})
	d := open(t, e, booking(t))
	err := d.call(context.Background(), func(pdfium.Pdfium) error { panic("boom") })
	if err == nil || strings.Contains(err.Error(), "boom") {
		t.Errorf("call error = %v, want an error without the panic value", err)
	}
	open(t, e, booking(t))
}

func TestEngine_Close(t *testing.T) {
	t.Parallel()
	e, err := New(Config{CacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close before start: %v", err)
	}
	if _, err := e.Open(context.Background(), booking(t)); !errors.Is(err, ErrClosed) {
		t.Errorf("Open after Close = %v, want ErrClosed", err)
	}
}

func TestCleanText(t *testing.T) {
	t.Parallel()
	got := cleanText("a\r\nb\rc\x02d\te￾f")
	if want := "a\nb\ncd\tef"; got != want {
		t.Errorf("cleanText = %q, want %q", got, want)
	}
}

// TestEngine_startTimes logs the cold (empty cache) and warm start of an engine and the
// per-page timings on booking.pdf; run with -v to read them.
func TestEngine_startTimes(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing; a cold compile takes about 13 s under -race")
	}
	data := booking(t)
	dir := t.TempDir()
	for _, label := range []string{"cold", "warm"} {
		e := newEngine(t, Config{CacheDir: dir})
		start := time.Now()
		d := open(t, e, data)
		opened := time.Since(start)
		start = time.Now()
		if _, err := d.Text(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		text := time.Since(start)
		start = time.Now()
		if _, err := d.Render(context.Background(), 0, 150); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: start+open %s, text %s, render+png 150 dpi %s", label, opened, text, time.Since(start))
		d.Close()
		_ = e.Close()
	}
	e := newEngine(t, Config{CacheDir: dir})
	d := open(t, e, pdftest.Build(pdftest.Pages(20, 1500)...))
	start := time.Now()
	for i := range d.PageCount() {
		if _, err := d.Text(context.Background(), i); err != nil {
			t.Fatal(err)
		}
	}
	text := time.Since(start)
	start = time.Now()
	for i := range d.PageCount() {
		if _, err := d.Render(context.Background(), i, 150); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("20 pages: text %s, render+png 150 dpi %s", text, time.Since(start))
}

func BenchmarkOpen(b *testing.B) {
	e := newEngine(b, Config{})
	data := booking(b)
	open(b, e, data).Close() // start the pool outside the loop
	for b.Loop() {
		d, err := e.Open(context.Background(), data)
		if err != nil {
			b.Fatal(err)
		}
		d.Close()
	}
}

func BenchmarkText(b *testing.B) {
	e := newEngine(b, Config{})
	d := open(b, e, booking(b))
	for b.Loop() {
		if _, err := d.Text(context.Background(), 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRender150(b *testing.B) {
	e := newEngine(b, Config{})
	d := open(b, e, booking(b))
	for b.Loop() {
		if _, err := d.Render(context.Background(), 0, 150); err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzOpen(f *testing.F) {
	f.Add(booking(f))
	f.Add(pdftest.Build(pdftest.Pages(2, 50)...))
	f.Add(pdftest.Encrypted(pdftest.TextPage("x")))
	f.Add([]byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF"))
	e := newEngine(f, Config{MaxPages: 5, MaxBytes: 1 << 20, Timeout: 10 * time.Second})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := e.Open(context.Background(), data)
		if err != nil {
			for _, want := range []error{ErrInvalid, ErrEncrypted, ErrTooManyPages, ErrTooLarge, ErrTimeout, ErrBusy} {
				if errors.Is(err, want) {
					return
				}
			}
			if strings.Contains(err.Error(), "pdfium call failed") {
				return
			}
			t.Fatalf("untyped error: %v", err)
		}
		defer d.Close()
		for i := range d.PageCount() {
			if _, err := d.Text(context.Background(), i); err != nil {
				return
			}
		}
	})
}

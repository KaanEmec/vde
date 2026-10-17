# Job: `internal/pdf` on go-pdfium (WebAssembly)

Version: v0.4 · Epic: 04 PDF preprocessing · Status: planned · Depends on: v0.3

## Goal

A small, bounded, cgo-free package that opens a PDF, counts its pages, reads each page's
text and renders a page to PNG at a given dpi.

## Context

The spike (ADR 0010) settled the API path. `webassembly.Init(webassembly.Config{MinIdle,
MaxIdle, MaxTotal, RuntimeConfig})` uses a runtime config with
`api.CoreFeaturesV2|experimental.CoreFeaturesExceptionHandling` and a
`wazero.NewCompilationCacheWithDir` cache. Then `pool.GetInstance(timeout)`,
`inst.OpenDocument(&requests.OpenDocument{File: &data})`, `FPDF_GetPageCount`,
`GetPageText(&requests.GetPageText{Page: ByIndex})` and
`RenderPageInDPI(&requests.RenderPageInDPI{DPI: 150, ...})`. The result's `RenderedImage`
is encoded with `image/png`, and then `Cleanup()` is called. An instance holds 230 to 490 MB
at peak, so the pool is small and starts lazily.

## Tasks

- [ ] `go.mod`: `github.com/klippa-app/go-pdfium v1.21.3` and
      `github.com/tetratelabs/wazero v1.12.x` (direct), plus go-commons-pool and
      google/uuid (indirect). Record `go mod graph | wc -l` in the job outcome. Add the rows
      to the dependency table in `docs/architecture/overview.md`.
- [ ] `internal/pdf/pdf.go`: `type Engine` (one per process) with `New(Options)`, where
      `Options{Workers int; CacheDir string; CallTimeout time.Duration; MaxPages int;
      MaxBytes int64}`. Methods: `Engine.Open(ctx, data) (*Doc, error)`, `Doc.Pages() int`,
      `Doc.Text(ctx, page) (string, error)`, `Doc.Render(ctx, page, dpi) ([]byte, error)`,
      `Doc.Close()`, and `Engine.Close()`. The engine initializes the pool on the first
      `Open` (`sync.Once`; an init error is returned every time after, not cached as a
      panic).
- [ ] Bounds: at most `Workers` instances (`MaxTotal`); `GetInstance` waits at most
      `CallTimeout`; every call runs under `ctx` and `CallTimeout`; documents above
      `MaxBytes` or with more than `MaxPages` pages are refused with typed errors
      (`ErrTooManyPages`, `ErrEncrypted`, `ErrInvalid`). An instance that times out or
      returns an internal error is closed and replaced, never reused. A panic inside a
      call becomes an error.
- [ ] Compilation cache under `server.data_dir/pdfium-cache` (created `0700`). The CLI
      uses the user cache dir (`os.UserCacheDir()/vde/pdfium-cache`). Without a writable
      directory it runs uncached, with a warning at start.
- [ ] Rendering: white background, no annotations beyond the default, no forms rendered
      interactively. Output is PNG. A JPEG conversion, when wanted, goes through the
      existing image policy in `internal/document/convert.go`, not here.
- [ ] Architectures: the wazero compiler on amd64 and arm64. Elsewhere it falls back to the
      interpreter (about 60 times slower), and `Engine` reports it so `config check` can
      warn.
- [ ] `NOTICE` at the repository root: go-pdfium (MIT), wazero (Apache-2.0), pdfium
      (BSD-3-Clause). The Dockerfile copies it into the image.
- [ ] `Dockerfile` and `make image`: build unchanged (`CGO_ENABLED=0`). Record the binary
      and image size before and after in the job outcome, and check the binary is under its
      v0.3 size plus 20 MB.

## Acceptance criteria

- Tests on `examples/samples/booking.pdf`: page count, non-empty text containing the
  sample's booking reference, and a page render whose PNG decodes to the expected size at
  150 dpi (A4 is 1240 × 1754).
- A generated multi-page PDF (written by the test from a tiny hand-made PDF template in
  `internal/pdf/testdata/`, 25 pages) is refused with `ErrTooManyPages` at `MaxPages: 20`.
  A 3-page one gives 3 texts in order.
- Corrupt bytes, an encrypted PDF and a zero-byte input give typed errors, never panics.
  `FuzzOpen` runs in CI for 30 s.
- Concurrency: 8 goroutines on `Workers: 1` all finish, serialized. A call that exceeds
  `CallTimeout` returns an error and the next call succeeds on a fresh instance.
- Benchmarks (`BenchmarkText`, `BenchmarkRender150`) are recorded in the job outcome, warm
  cache, against the ADR 0010 budget.
- `go build` with `CGO_ENABLED=0` for linux/amd64 and linux/arm64 in CI.

## Out of scope

OCR, form filling, annotations, page extraction or merging, HEIC.

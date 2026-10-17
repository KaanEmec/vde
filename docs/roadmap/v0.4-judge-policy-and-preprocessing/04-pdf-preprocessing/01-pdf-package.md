# Job: `internal/pdf` on go-pdfium (WebAssembly)

Version: v0.4 · Epic: 04 PDF preprocessing · Status: done · Depends on: v0.3

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
- [x] `internal/pdf/pdf.go`: `type Engine` (one per process) with `New(Options)`, where
      `Options{Workers int; CacheDir string; CallTimeout time.Duration; MaxPages int;
      MaxBytes int64}`. Methods: `Engine.Open(ctx, data) (*Doc, error)`, `Doc.Pages() int`,
      `Doc.Text(ctx, page) (string, error)`, `Doc.Render(ctx, page, dpi) ([]byte, error)`,
      `Doc.Close()`, and `Engine.Close()`. The engine initializes the pool on the first
      `Open` (`sync.Once`; an init error is returned every time after, not cached as a
      panic).
- [x] Bounds: at most `Workers` instances (`MaxTotal`); `GetInstance` waits at most
      `CallTimeout`; every call runs under `ctx` and `CallTimeout`; documents above
      `MaxBytes` or with more than `MaxPages` pages are refused with typed errors
      (`ErrTooManyPages`, `ErrEncrypted`, `ErrInvalid`). An instance that times out or
      returns an internal error is closed and replaced, never reused. A panic inside a
      call becomes an error.
- [x] Compilation cache under `server.data_dir/pdfium-cache` (created `0700`). The CLI
      uses the user cache dir (`os.UserCacheDir()/vde/pdfium-cache`). Without a writable
      directory it runs uncached, with a warning at start.
- [x] Rendering: white background, no annotations beyond the default, no forms rendered
      interactively. Output is PNG. A JPEG conversion, when wanted, goes through the
      existing image policy in `internal/document/convert.go`, not here.
- [x] Architectures: the wazero compiler on amd64 and arm64. Elsewhere it falls back to the
      interpreter (about 60 times slower), and `Engine` reports it so `config check` can
      warn.
- [x] `NOTICE` at the repository root: go-pdfium (MIT), wazero (Apache-2.0), pdfium
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

## Measurements

Apple M-series laptop (darwin/arm64), Go 1.27.1, 2026-10-17.

| Item | Measured |
| --- | --- |
| Binary, `-trimpath -ldflags "-s -w"`, `CGO_ENABLED=0` | darwin/arm64 25.6 → 34.8 MB (+9.2 MB); linux/amd64 26.8 → 35.9 MB (+9.0 MB); linux/arm64 34.1 MB. Under the v0.3 size plus 20 MB. |
| `go mod graph` | 305 → 350 lines (+45): go-pdfium, wazero, go-commons-pool and their test-only requirements; google/uuid was already there |
| Cold start (empty compilation cache): pool start, instance, open booking.pdf | 1.17 to 1.19 s (about 13 s under `-race`) |
| Warm start (cache filled) | 35 ms |
| `BenchmarkOpen` (fresh instance per document, booking.pdf) | 0.54 ms |
| `BenchmarkText` (booking.pdf, one page) | 0.47 ms; the first text call of a document 3 ms |
| `BenchmarkRender150` (A4 at 150 dpi, PNG `BestSpeed`) | 15.9 ms |
| 20-page text-rich PDF (generated) | text of all pages 33 ms, render and encode of all pages 0.41 s: within the 2 s budget |
| Owner suite (5 PDFs, 1 to 5 pages) | text layer 10 to 45 ms per PDF after start; rendering all pages 0.1 to 0.55 s |
| `FuzzOpen`, 20 s locally | 61 000 inputs, no failure |

## Deviations

- `Config` (the job's `Options`) is `Config{Workers, CacheDir, Timeout, MaxPages, MaxBytes}`;
  the page count is `Doc.PageCount()`, and `Doc.Text` takes a context. The pool starts
  under a mutex rather than a `sync.Once`, so `Close` before the first `Open` is safe; an
  init error is still kept and returned every time. A process-wide mutex also serializes
  engine starts, because wazero's lazily computed version is not safe for concurrent first
  use (found by `-race` with two engines).
- go-pdfium's pool does not reuse instances (`ReuseWorkers: false`): every `Open` gets a
  fresh instance (0.5 ms), so pdfium's WebAssembly memory, which never shrinks, is
  released after each document. `Doc` holds its instance until `Close`. A call that times
  out, fails or panics kills the instance (`Kill` with `WithCloseOnContextDone`, which
  stops the running WebAssembly) and the `Doc` answers `ErrClosed` afterwards.
- Extra bounds: WebAssembly memory is capped at 1 GiB per instance, a page whose bitmap
  would exceed 40 megapixels is refused (`ErrPageTooLarge`), dpi is limited to 36 to 600,
  the sandbox mounts no host directory (go-pdfium mounts `/` by default) and pdfium's
  stdout and stderr are discarded. Extra typed errors: `ErrTooLarge`, `ErrPageRange`,
  `ErrTimeout`, `ErrClosed`.
- A4 at 150 dpi renders to 1240 × 1755, not 1754: pdfium rounds 842 pt × 150 / 72 =
  1754.2 up. The test expects 1755.
- Test PDFs come from a small generator, `internal/pdf/pdftest` (text pages, an image-only
  page, an encryption dictionary), instead of a template in `internal/pdf/testdata/`; the
  document and CLI tests use it too.
- `Compiled()` is a package function (GOOS and GOARCH, mirroring wazero's check), used by
  `config check`; the engine does not report it.
- Text is normalized in `Doc.Text`: CRLF to LF, control characters other than tab and
  newline dropped.
- Carried to the epic 05 closeout (unticked above), outside this wave's files: `make image`
  sizes (no image was built), and running `FuzzOpen` and the linux/amd64 and linux/arm64
  builds in CI. The dependency row in `docs/architecture/overview.md` and copying `NOTICE`
  (with `LICENSE`) into the image were done with epic 03's wiring (job 03-03). Both cross builds succeed locally. `go.mod`
  lists go-pdfium and wazero as `// indirect` until the next `go mod tidy`.

## Out of scope

OCR, form filling, annotations, page extraction or merging, HEIC.

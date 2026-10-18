package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/pdf"
)

// newPDFEngine builds the process's pdfium engine from documents.pdf (ADR 0010). It
// starts on the first PDF that needs it, so a process that never prepares a PDF never
// compiles the module. cacheDir holds the compiled code: server.data_dir/pdfium-cache
// for serve (pdf.CacheDir), cliPDFCacheDir for the CLI.
func newPDFEngine(cfg *config.Config, cacheDir string) (*pdf.Engine, error) {
	return pdf.New(pdf.Config{
		Workers:  cfg.Documents.PDF.Workers,
		CacheDir: cacheDir,
		MaxPages: cfg.Documents.PDF.MaxPages,
		MaxBytes: cfg.Limits.DocumentBytes,
	})
}

// cliPDFCacheDir is the compilation cache of CLI commands, under the user cache
// directory; "" (uncached) when there is none.
func cliPDFCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "vde", "pdfium-cache")
}

// warmPDF compiles the pdfium module before serve accepts requests when warm is set (a
// PDF mode other than native), so no submit pays the cold start, then logs why the
// compilation cache is off, if it is: New finds an unusable directory, the start a cache
// wazero cannot open. The engine keeps a failed start, so a failed warm-up leaves PDF
// preparation unavailable until the process restarts.
func warmPDF(ctx context.Context, logger *slog.Logger, engine *pdf.Engine, warm bool) {
	if warm {
		start := time.Now()
		if err := engine.Warm(ctx); err != nil {
			logger.Warn("pdfium warm-up failed; PDF preparation is unavailable until restart", "error", err)
		} else {
			logger.Info("pdfium ready", "duration_ms", time.Since(start).Milliseconds())
		}
	}
	if cerr := engine.CacheErr(); cerr != nil {
		logger.Warn("pdfium compilation cache unavailable; PDFs are prepared uncached (slower first start)", "error", cerr)
	}
}

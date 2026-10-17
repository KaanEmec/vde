package main

import (
	"os"
	"path/filepath"

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

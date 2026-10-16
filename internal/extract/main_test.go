package extract

import (
	"log/slog"
	"os"
	"testing"
)

// TestMain keeps the loop's warnings out of the test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

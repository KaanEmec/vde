// Package version holds the build information injected with -ldflags at build time.
package version

// Set with -ldflags "-X github.com/kaanemec/vde/internal/version.Version=..." by the Makefile.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns the version line printed by vde version.
func String() string {
	return "vde " + Version + " (commit " + Commit + ", built " + Date + ")"
}

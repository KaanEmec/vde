// Command vde is the verified document extraction service and its operator tools.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/kaanemec/vde/internal/version"
)

// Exit codes: 0 success, 1 failure (including an invalid configuration), 2 usage error.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `Usage: vde <command> [arguments]

Commands:
  version    print version, commit and build date
  help       print this help

Run "vde <command> -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches to a subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, version.String())
		return exitOK
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "vde: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

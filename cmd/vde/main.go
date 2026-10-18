// Command vde is the verified document extraction service and its operator tools.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/kaanemec/vde/internal/version"
)

// Exit codes: 0 success (vde extract: accepted), 1 usage or configuration error or other
// failure, 2 rejected and 3 failed (vde extract only).
const (
	exitOK        = 0
	exitFailure   = 1
	exitUsage     = 1
	exitRejected  = 2
	exitFailed    = 3
	exitExtracted = 4 // vde extract: a result no judge verified (judge policy never)
)

const usage = `Usage: vde <command> [arguments]

Commands:
  serve        run the HTTP service (vde serve -h)
  extract      run one extraction on a document file (vde extract -h)
  eval         measure extraction quality on a corpus (vde eval -h)
  config       check the configuration file or print its JSON Schema (vde config check -h)
  app          list the applications in the database (vde app list -h)
  token        create, list and revoke access and admin tokens (vde token -h)
  audit        list the audit log (vde audit list -h)
  migrate      apply pending database migrations and print the schema version
  healthcheck  request /healthz on the configured listener
  version      print version, commit and build date
  help         print this help

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
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "extract":
		return runExtract(args[1:], stdout, stderr)
	case "eval":
		return runEval(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "app":
		return runApp(args[1:], stdout, stderr)
	case "token":
		return runToken(args[1:], stdout, stderr)
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(args[1:], stdout, stderr)
	case "healthcheck":
		return runHealthcheck(args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "vde: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

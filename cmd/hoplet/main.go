// Command hoplet is Hoplet's single binary: daemon, shim, relay and CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yangflow/hoplet/internal/shim"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: hoplet <command>\n\ncommands:\n  version  print the version\n  shim     run a session's harness process (started by the daemon)")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "shim":
		return runShim(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "hoplet: unknown command %q\n", args[0])
		return 2
	}
}

// shimUsage is printed for `hoplet shim --help` and on usage errors.
const shimUsage = `usage: hoplet shim --dir DIR --cwd DIR [--linger D] -- COMMAND [ARGS...]

The daemon must start the shim in its own session (setsid) with stdin on
/dev/null, stdout and stderr on DIR/shim.log, and a clean environment free of
credentials and HOPLET_* internals: the harness inherits that environment.
The shim ignores SIGHUP, SIGINT and SIGPIPE and stops on SIGTERM, which
terminates the harness's process group first.
`

// runShim parses `hoplet shim --dir DIR --cwd DIR [--linger D] -- COMMAND
// [ARGS...]`. The child inherits the shim's environment; SIGTERM to the shim
// terminates the child's process group and exits.
func runShim(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("shim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, shimUsage)
		fs.PrintDefaults()
	}
	dir := fs.String("dir", "", "session directory for the spool, socket and lock")
	cwd := fs.String("cwd", "", "working directory of the harness process")
	linger := fs.Duration("linger", shim.DefaultLinger, "how long to wait for a final ack after the child exits")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *dir == "" || *cwd == "" || fs.NArg() == 0 {
		fmt.Fprint(stderr, shimUsage)
		return 2
	}
	shim.IgnoreParentSignals()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	cfg := shim.Config{
		Dir: *dir, WorkDir: *cwd, Command: fs.Args(), Env: os.Environ(), Linger: *linger,
		Logger: slog.New(slog.NewJSONHandler(stderr, nil)),
	}
	if err := shim.Run(ctx, cfg); err != nil {
		fmt.Fprintf(stderr, "hoplet shim: %v\n", err)
		return 1
	}
	return 0
}

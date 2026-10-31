// Command hoplet is Hoplet's single binary: daemon, shim, relay and CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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

// runShim parses `hoplet shim --dir DIR [--linger D] -- COMMAND [ARGS...]`.
// The child inherits the shim's environment; SIGTERM or SIGINT to the shim
// terminates the child's process group and exits.
func runShim(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("shim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "session directory for the spool and socket")
	linger := fs.Duration("linger", shim.DefaultLinger, "how long to wait for a final ack after the child exits")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: hoplet shim --dir DIR [--linger D] -- COMMAND [ARGS...]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	cfg := shim.Config{Dir: *dir, Command: fs.Args(), Env: os.Environ(), Linger: *linger}
	if err := shim.Run(ctx, cfg); err != nil {
		fmt.Fprintf(stderr, "hoplet shim: %v\n", err)
		return 1
	}
	return 0
}

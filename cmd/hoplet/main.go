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

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/claude"
	"github.com/yangflow/hoplet/internal/launchd"
	"github.com/yangflow/hoplet/internal/shim"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `usage: hoplet <command>

commands:
  session   create, follow and drive sessions (hoplet session help)
  daemon    run the daemon, or install, uninstall or inspect its LaunchAgent
  harness   install or upgrade Hoplet's copy of a harness (hoplet harness help)
  env       refresh the environment harnesses start with
  doctor    check the installation, the daemon and its environment
  version   print the version
  shim      run a session's harness process (started by the daemon)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// app is one invocation of the command line.
type app struct {
	stdout, stderr io.Writer
	// harnesses returns the adapters `hoplet daemon` serves for a home.
	harnesses func(home string) map[string]harness.Harness
	// env, when set, replaces the login-shell environment of `hoplet daemon`
	// (tests).
	env env.Source
	// probeURL, when set, replaces what the daemon's doctor reaches (tests).
	probeURL string
	// agents returns the LaunchAgent manager; run sets the real one.
	agents func() (launchd.Manager, error)
	// allowTempBinary lets `daemon install` use a binary under the temp
	// directory (tests).
	allowTempBinary bool
	// selfCheck checks a claude binary before Hoplet switches to it; nil
	// also turns the daemon's daily follow-up off (tests).
	selfCheck func(ctx context.Context, bin string, env []string) error
}

func run(args []string, stdout, stderr io.Writer) int {
	return app{stdout: stdout, stderr: stderr, harnesses: harnesses, agents: realAgents, selfCheck: claude.SelfCheck}.run(args)
}

// harnesses are the adapters a production daemon serves: each runs
// Hoplet's own, version-pinned copy of its harness (hoplet harness install).
func harnesses(home string) map[string]harness.Harness {
	return map[string]harness.Harness{claude.Kind: &claude.Harness{Binary: claude.BinaryPath(home)}}
}

func (a app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(a.stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, usage)
		return 0
	case "session":
		return a.session(args[1:])
	case "daemon":
		return a.daemon(args[1:])
	case "harness":
		return a.harnessCmd(args[1:])
	case "dev":
		return a.dev(args[1:])
	case "env":
		return a.envCmd(args[1:])
	case "doctor":
		return a.doctor(args[1:])
	case "shim":
		return runShim(args[1:], a.stderr)
	default:
		fmt.Fprintf(a.stderr, "hoplet: unknown command %q\n", args[0])
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

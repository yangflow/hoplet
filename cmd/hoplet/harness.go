package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness/claude"
	"github.com/yangflow/hoplet/internal/home"
)

const harnessUsage = `usage: hoplet harness install | upgrade claude [--from PATH] [--json]

The harness name comes before the flags: hoplet harness install claude --from PATH

Copies the claude binary (by default the one on your PATH) into Hoplet's
home and points Hoplet at it; your logins in ~/.claude stay shared and
claude's own updater is off for Hoplet's copy. A version the Claude Code
adapter has no recordings of is first run through a self-check on this
machine (a few short turns on the cheapest model, using your claude login);
if it fails, Hoplet keeps its current copy. The check needs claude to ask
before running a shell command: if your own settings or hooks auto-allow
it, the check fails and Hoplet stays on its current copy. A running daemon picks the copy
up for new sessions.

The daemon also does this once a day for the claude on your PATH, unless
config.toml sets [harness.claude] follow = false. hoplet doctor shows the
outcome.
`

// selfCheckTimeout bounds install and upgrade, self-check included.
const selfCheckTimeout = 10 * time.Minute

func (a app) harnessCmd(args []string) int {
	if len(args) < 2 || (args[0] != "install" && args[0] != "upgrade") || args[1] != claude.Kind {
		if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
			fmt.Fprint(a.stdout, harnessUsage)
			return 0
		}
		fmt.Fprint(a.stderr, harnessUsage)
		return 2
	}
	name := "harness " + args[0]
	fs := a.flags(name, harnessUsage)
	from := fs.String("from", "", "the claude binary to copy (default: claude on PATH)")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args[2:], 0, 0); !ok {
		return code
	}
	if *from == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			return a.fail(name, errors.New("no claude on PATH; give --from"))
		}
		*from = p
	}
	h, err := home.Dir()
	if err != nil {
		return a.fail(name, err)
	}
	if err := os.MkdirAll(h, 0o700); err != nil {
		return a.fail(name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfCheckTimeout)
	defer cancel()
	vars, err := a.harnessEnv(ctx, h)
	if err != nil {
		return a.fail(name, fmt.Errorf("read the harness environment: %w", err))
	}
	check := func(ctx context.Context, bin string) error {
		if a.selfCheck == nil {
			return errors.New("this build has no self-check")
		}
		fmt.Fprintln(a.stderr, "running the self-check (about a minute)...")
		return a.selfCheck(ctx, bin, vars)
	}
	got, err := claude.Install(ctx, *from, h, check)
	if err != nil {
		return a.fail(name, err)
	}
	if *asJSON {
		a.printJSON(map[string]any{"harness": claude.Kind, "version": got.Version, "checked": got.Checked, "path": claude.BinaryPath(h)})
		return 0
	}
	if got.Checked {
		fmt.Fprintf(a.stdout, "claude %s passed the self-check\n", got.Version)
	}
	fmt.Fprintf(a.stdout, "installed claude %s at %s\n", got.Version, claude.BinaryPath(h))
	return 0
}

// harnessEnv is the environment the daemon of home starts harnesses with.
func (a app) harnessEnv(ctx context.Context, h string) ([]string, error) {
	src := a.env
	if src == nil {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		src = &env.Login{Home: h, UserHome: userHome, Shell: env.UserShell(ctx)}
	}
	snap, err := src.Environment(ctx, false)
	if err != nil {
		return nil, err
	}
	return snap.Vars, nil
}

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
	"github.com/yangflow/hoplet/internal/harness/codex"
	"github.com/yangflow/hoplet/internal/harness/hermes"
	"github.com/yangflow/hoplet/internal/home"
)

const harnessUsage = `usage: hoplet harness install | upgrade claude [--from PATH] [--json]
       hoplet harness install | upgrade codex [--json]
       hoplet harness verify hermes [--json]

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

install codex downloads the official Codex package of the version Hoplet is
pinned to from GitHub (about 170 MB), checks it against the release's
published SHA-256 and keeps it in Hoplet's home. Your Codex login and
configuration stay yours, and the Codex you run in a terminal is untouched.
Nothing is switched if the check fails.

verify hermes runs a similar self-check on the Hermes your PATH finds (an
edit asked and denied, the same edit in full_auto, an interrupt; on your
Hermes model, leaving a short session in its history). Hoplet starts new
Hermes turns only on versions its recordings cover or that passed here.
The daemon checks a new Hermes version once a day by itself, unless
config.toml sets [harness.hermes] follow = false.
`

// selfCheckTimeout bounds install and upgrade, self-check included.
const selfCheckTimeout = 10 * time.Minute

// codexInstallTimeout bounds downloading and unpacking the Codex package.
const codexInstallTimeout = 30 * time.Minute

func (a app) harnessCmd(args []string) int {
	if len(args) >= 2 && args[0] == "verify" && args[1] == hermes.Kind {
		return a.harnessVerify(args[2:])
	}
	if len(args) >= 2 && (args[0] == "install" || args[0] == "upgrade") && args[1] == codex.Kind {
		return a.harnessInstallCodex(args[0], args[2:])
	}
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

// harnessInstallCodex installs the pinned official Codex package.
func (a app) harnessInstallCodex(verb string, args []string) int {
	name := "harness " + verb
	fs := a.flags(name, harnessUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	h, err := home.Dir()
	if err != nil {
		return a.fail(name, err)
	}
	if err := os.MkdirAll(h, 0o700); err != nil {
		return a.fail(name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexInstallTimeout)
	defer cancel()
	if !*asJSON {
		fmt.Fprintf(a.stderr, "fetching Codex %s...\n", codex.PinnedVersion)
	}
	got, err := codex.Install(ctx, h, codex.Source{})
	if err != nil {
		return a.fail(name, err)
	}
	if *asJSON {
		a.printJSON(map[string]any{"harness": codex.Kind, "version": got.Version, "voice": got.Voice, "path": codex.BinaryPath(h)})
		return 0
	}
	fmt.Fprintf(a.stdout, "installed Codex %s at %s\n", got.Version, codex.BinaryPath(h))
	if !got.Voice {
		fmt.Fprintln(a.stdout, "this package has no voice component")
	}
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

// harnessVerify runs `hoplet harness verify hermes`.
func (a app) harnessVerify(args []string) int {
	const name = "harness verify"
	fs := a.flags(name, harnessUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if a.hermesCheck == nil {
		return a.fail(name, errors.New("this build has no self-check"))
	}
	h, err := home.Dir()
	if err != nil {
		return a.fail(name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfCheckTimeout)
	defer cancel()
	vars, err := a.harnessEnv(ctx, h)
	if err != nil {
		return a.fail(name, fmt.Errorf("read the harness environment: %w", err))
	}
	// Say so only when a check really runs: Verify may find the version
	// covered or recorded.
	check := func(ctx context.Context, bin string, env []string) error {
		fmt.Fprintln(a.stderr, "checking Hermes (a few minutes at most)...")
		return a.hermesCheck(ctx, bin, env)
	}
	o, checked, err := hermes.Verify(ctx, hermes.StateDir(h), vars, check, true, func() { fmt.Fprintln(a.stderr, "waiting for another Hermes check to finish...") })
	if err != nil {
		return a.fail(name, err)
	}
	if *asJSON {
		a.printJSON(map[string]any{"harness": hermes.Kind, "version": o.Version, "checked": checked, "error": o.Error})
	} else {
		switch {
		case !checked:
			fmt.Fprintf(a.stdout, "hermes %s needs no check: Hoplet's recordings cover it, or it passed here before\n", o.Version)
		case o.Error == "":
			fmt.Fprintf(a.stdout, "hermes %s passed the self-check\n", o.Version)
		default:
			fmt.Fprintf(a.stdout, "hermes %s failed the self-check: %s\n", o.Version, o.Error)
		}
	}
	if o.Error != "" {
		return 1
	}
	return 0
}

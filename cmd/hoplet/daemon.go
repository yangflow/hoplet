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

	"github.com/yangflow/hoplet/internal/daemon"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/home"
)

const daemonUsage = `usage: hoplet daemon [--home DIR]

Runs the daemon in the foreground until SIGTERM or SIGINT. The home
directory defaults to $HOPLET_HOME, else ~/.hoplet.
`

// harnessEnvKeys are the daemon's environment variables passed on to
// harness processes.
var harnessEnvKeys = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TMPDIR"}

func runDaemon(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, daemonUsage)
		fs.PrintDefaults()
	}
	dir := fs.String("home", "", "home directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, daemonUsage)
		return 2
	}
	if *dir == "" {
		d, err := home.Dir()
		if err != nil {
			fmt.Fprintf(stderr, "hoplet daemon: %v\n", err)
			return 1
		}
		*dir = d
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "hoplet daemon: %v\n", err)
		return 1
	}
	var vars []string
	for _, k := range harnessEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			vars = append(vars, k+"="+v)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	d, err := daemon.Start(ctx, daemon.Config{
		Home: *dir, Version: version, Exe: exe,
		// Adapters register here as they land.
		Harnesses: map[string]harness.Harness{},
		Env:       env.Static(vars),
		Logger:    slog.New(slog.NewJSONHandler(stderr, nil)),
	})
	if err != nil && ctx.Err() != nil {
		return 0 // stopped by a signal before it began serving
	}
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintf(stderr, "hoplet daemon: a daemon is already running for %s\n", *dir)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "hoplet daemon: %v\n", err)
		return 1
	}
	select {
	case <-ctx.Done():
	case <-d.Done():
	}
	// A second signal while closing kills the process the default way.
	stop()
	if err := errors.Join(d.Close(), d.Err()); err != nil {
		fmt.Fprintf(stderr, "hoplet daemon: %v\n", err)
		return 1
	}
	return 0
}

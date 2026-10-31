package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/daemon"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/launchd"
	"github.com/yangflow/hoplet/internal/logfile"
)

const daemonUsage = `usage: hoplet daemon [--home DIR] [--log FILE]
       hoplet daemon install | uninstall
       hoplet daemon status [--json]

Without a subcommand, runs the daemon in the foreground until SIGTERM or
SIGINT, logging to stderr or, with --log, to a rotated file. The home
directory defaults to $HOPLET_HOME, else ~/.hoplet.

install   install this binary as a LaunchAgent (macOS) and start it
uninstall stop the LaunchAgent and remove it
status    show the LaunchAgent and whether the daemon serves
`

// readyWait bounds how long install waits for the daemon to serve.
const readyWait = 15 * time.Second

func (a app) daemon(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return a.daemonInstall(args[1:])
		case "uninstall":
			return a.daemonUninstall(args[1:])
		case "status":
			return a.daemonStatus(args[1:])
		}
	}
	return a.daemonRun(args)
}

func (a app) daemonRun(args []string) int {
	fs := a.flags("daemon", daemonUsage)
	dir := fs.String("home", "", "home directory")
	logPath := fs.String("log", "", "log to this file, rotated, instead of stderr")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if *dir == "" {
		d, err := home.Dir()
		if err != nil {
			return a.fail("daemon", err)
		}
		*dir = d
	}
	exe, err := os.Executable()
	if err != nil {
		return a.fail("daemon", err)
	}
	logw := a.stderr
	if *logPath != "" {
		f, err := logfile.Open(*logPath, 0, 0)
		if err != nil {
			return a.fail("daemon", err)
		}
		defer f.Close()
		logw = f
	}
	src := a.env
	if src == nil {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return a.fail("daemon", err)
		}
		src = &env.Login{Home: *dir, UserHome: userHome, Shell: env.DefaultShell()}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	d, err := daemon.Start(ctx, daemon.Config{
		Home: *dir, Version: version, Exe: exe, Harnesses: a.harnesses, Env: src, ProbeURL: a.probeURL,
		Logger: slog.New(slog.NewJSONHandler(logw, nil)),
	})
	if err != nil && ctx.Err() != nil {
		return 0 // stopped by a signal before it began serving
	}
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintf(a.stderr, "hoplet daemon: a daemon is already running for %s\n", *dir)
		return 1
	}
	if err != nil {
		return a.fail("daemon", err)
	}
	select {
	case <-ctx.Done():
	case <-d.Done():
	}
	// A second signal while closing kills the process the default way.
	stop()
	if err := errors.Join(d.Close(), d.Err()); err != nil {
		return a.fail("daemon", err)
	}
	return 0
}

// agents returns the LaunchAgent manager of the current user.
func agents() (launchd.Manager, error) {
	if runtime.GOOS != "darwin" {
		return launchd.Manager{}, errors.New("installing the daemon is only supported on macOS for now")
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return launchd.Manager{}, err
	}
	return launchd.Manager{UserHome: userHome, UID: os.Getuid(), Run: runCommand}, nil
}

func (a app) daemonInstall(args []string) int {
	fs := a.flags("daemon install", daemonUsage)
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	m, err := agents()
	if err != nil {
		return a.fail("daemon install", err)
	}
	h, err := home.Dir()
	if err != nil {
		return a.fail("daemon install", err)
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return a.fail("daemon install", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), readyWait+10*time.Second)
	defer cancel()
	if err := m.Install(ctx, launchd.Agent{Exe: exe, Home: h}); err != nil {
		return a.fail("daemon install", err)
	}
	c, err := waitServing(ctx, h)
	if err != nil {
		return a.fail("daemon install", fmt.Errorf("installed %s, but the daemon does not serve: %w (see %s)", m.Path(), err, launchd.LogFile(h)))
	}
	defer c.Close()
	fmt.Fprintf(a.stdout, "installed %s\ndaemon %s serving at %s\n", m.Path(), c.Hello().Version, home.Socket(h))
	return 0
}

// waitServing dials the home's daemon until it answers or ctx ends.
func waitServing(ctx context.Context, h string) (*client.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, readyWait)
	defer cancel()
	for {
		c, err := dialHome(ctx, h)
		if err == nil {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a app) daemonUninstall(args []string) int {
	fs := a.flags("daemon uninstall", daemonUsage)
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	m, err := agents()
	if err != nil {
		return a.fail("daemon uninstall", err)
	}
	if err := m.Uninstall(context.Background()); err != nil {
		return a.fail("daemon uninstall", err)
	}
	fmt.Fprintln(a.stdout, "uninstalled; running sessions keep their shims and resume when a daemon starts again")
	return 0
}

// daemonState is `hoplet daemon status`.
type daemonState struct {
	Socket      string          `json:"socket"`
	Serving     bool            `json:"serving"`
	Version     string          `json:"version,omitempty"`
	Host        string          `json:"host,omitempty"`
	Error       string          `json:"error,omitempty"`
	LaunchAgent *launchd.Status `json:"launch_agent,omitempty"`
}

func (a app) daemonStatus(args []string) int {
	fs := a.flags("daemon status", daemonUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	h, err := home.Dir()
	if err != nil {
		return a.fail("daemon status", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := daemonState{Socket: home.Socket(h)}
	if c, err := dialHome(ctx, h); err != nil {
		st.Error = err.Error()
	} else {
		st.Serving, st.Version, st.Host = true, c.Hello().Version, c.Hello().Host
		c.Close()
	}
	if m, err := agents(); err == nil {
		if s, err := m.Status(ctx); err == nil {
			st.LaunchAgent = &s
		}
	}
	if *asJSON {
		_ = json.NewEncoder(a.stdout).Encode(st)
	} else {
		if st.Serving {
			fmt.Fprintf(a.stdout, "daemon %s serving at %s (host %s)\n", st.Version, st.Socket, st.Host)
		} else {
			fmt.Fprintf(a.stdout, "daemon not serving at %s: %s\n", st.Socket, st.Error)
		}
		if s := st.LaunchAgent; s != nil {
			switch {
			case !s.Installed:
				fmt.Fprintln(a.stdout, "LaunchAgent not installed (hoplet daemon install)")
			case !s.Loaded:
				fmt.Fprintf(a.stdout, "LaunchAgent installed (%s) but not loaded\n", s.Program)
			default:
				fmt.Fprintf(a.stdout, "LaunchAgent %s, pid %d, last exit %s (%s)\n", s.State, s.PID, s.LastExit, s.Program)
			}
		}
	}
	if !st.Serving {
		return 1
	}
	return 0
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/yangflow/hoplet/internal/launchd"
	"github.com/yangflow/hoplet/internal/systemd"
	"github.com/yangflow/hoplet/proto"
)

// service is how the daemon is installed on this system: a LaunchAgent on
// macOS, a systemd user service on Linux.
type service interface {
	// Path is the file that defines the service; pathKey names it in
	// `daemon install --json`.
	Path() string
	pathKey() string
	Install(ctx context.Context, exe, home string) error
	Uninstall(ctx context.Context) error
	// logs says where to look when the installed daemon does not serve.
	logs(home string) string
	// installed writes to w what the user should know after an install
	// whose daemon serves.
	installed(ctx context.Context, w io.Writer)
	// status adds the service's state to st.
	status(ctx context.Context, st *daemonState)
	// describe writes st's service state for people.
	describe(w io.Writer, st daemonState)
	check(ctx context.Context) proto.Check
}

// realService returns the daemon's service for the current user.
func realService() (service, error) {
	switch runtime.GOOS {
	case "darwin":
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		return launchdService{launchd.Manager{UserHome: userHome, UID: os.Getuid(), Run: runCommand}}, nil
	case "linux":
		conf, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		u, err := user.Current()
		if err != nil {
			return nil, err
		}
		return newSystemdService(conf, systemd.LingerDir, u.Username, runCommand), nil
	}
	return nil, errors.New("installing the daemon is supported on macOS and Linux")
}

// newSystemdService is the daemon's user service for user, whose
// configuration directory is conf.
func newSystemdService(conf, lingerDir, username string, run systemd.Runner) systemdService {
	return systemdService{m: systemd.Manager{
		Dir: filepath.Join(conf, "systemd", "user"), Name: systemd.DaemonUnit, User: true, Run: run,
		Linger: filepath.Join(lingerDir, username),
	}, user: username}
}

// launchdService is the daemon as a macOS LaunchAgent.
type launchdService struct{ m launchd.Manager }

func (s launchdService) Path() string    { return s.m.Path() }
func (s launchdService) pathKey() string { return "plist" }

func (s launchdService) Install(ctx context.Context, exe, home string) error {
	return s.m.Install(ctx, launchd.Agent{Exe: exe, Home: home})
}

func (s launchdService) Uninstall(ctx context.Context) error { return s.m.Uninstall(ctx) }
func (s launchdService) logs(home string) string             { return launchd.LogFile(home) }

func (s launchdService) installed(ctx context.Context, w io.Writer) {
	if st, err := s.m.Status(ctx); err == nil && !agentRunning(st) {
		fmt.Fprintln(w, "hoplet daemon install: a daemon serves, but not the LaunchAgent's (another daemon may hold the home)")
	}
}

func (s launchdService) status(ctx context.Context, st *daemonState) {
	if ls, err := s.m.Status(ctx); err == nil {
		st.LaunchAgent = &ls
	}
}

func (s launchdService) describe(w io.Writer, st daemonState) {
	switch ls := st.LaunchAgent; {
	case ls == nil:
	case !ls.Installed:
		fmt.Fprintln(w, "LaunchAgent not installed (hoplet daemon install)")
	case !ls.Loaded:
		fmt.Fprintf(w, "LaunchAgent installed (%s) but not loaded\n", ls.Program)
	default:
		fmt.Fprintf(w, "LaunchAgent %s, pid %d, last exit %s (%s)\n", ls.State, ls.PID, ls.LastExit, ls.Program)
	}
}

// agentRunning reports whether launchd runs the agent's daemon.
func agentRunning(s launchd.Status) bool { return s.Loaded && s.State == "running" }

func (s launchdService) check(ctx context.Context) proto.Check {
	const name = "launch agent"
	ls, err := s.m.Status(ctx)
	if err != nil {
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: err.Error()}
	}
	switch {
	case !ls.Installed:
		return proto.Check{Name: name, Status: proto.CheckWarn, Detail: "not installed; the daemon will not start at login (hoplet daemon install)"}
	case !ls.Loaded:
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: s.m.Path() + " is installed but not loaded (hoplet daemon install)"}
	case otherBinary(ls.Program) != "":
		return proto.Check{Name: name, Status: proto.CheckWarn, Detail: fmt.Sprintf("runs %s, not this binary %s", ls.Program, otherBinary(ls.Program))}
	default:
		return proto.Check{Name: name, Status: proto.CheckOK, Detail: fmt.Sprintf("%s, pid %d", ls.State, ls.PID)}
	}
}

// systemdService is the daemon as a systemd user service.
type systemdService struct {
	m    systemd.Manager
	user string
}

func (s systemdService) Path() string    { return s.m.Path() }
func (s systemdService) pathKey() string { return "unit" }

func (s systemdService) Install(ctx context.Context, exe, home string) error {
	if err := os.MkdirAll(filepath.Dir(systemd.LogFile(home)), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	unit, err := systemd.Daemon(systemd.Agent{Exe: exe, Home: home})
	if err != nil {
		return err
	}
	return s.m.Install(ctx, unit)
}

func (s systemdService) Uninstall(ctx context.Context) error { return s.m.Uninstall(ctx) }

func (s systemdService) logs(home string) string {
	return systemd.LogFile(home) + " and journalctl --user -u " + systemd.DaemonUnit
}

// lingerHint is what to run so that the daemon outlives the user's login
// sessions.
func (s systemdService) lingerHint() string { return "sudo loginctl enable-linger " + s.user }

func (s systemdService) installed(ctx context.Context, w io.Writer) {
	st, err := s.m.Status(ctx)
	if err != nil {
		return
	}
	if !st.Running() {
		fmt.Fprintln(w, "hoplet daemon install: a daemon serves, but not the systemd service's (another daemon may hold the home)")
	}
	if !st.Linger {
		fmt.Fprintf(w, "hoplet daemon install: lingering is off for %s, so systemd stops the daemon when you log out; to keep it running: %s\n", s.user, s.lingerHint())
	}
}

func (s systemdService) status(ctx context.Context, st *daemonState) {
	if ss, err := s.m.Status(ctx); err == nil {
		st.Systemd = &ss
	}
}

func (s systemdService) describe(w io.Writer, st daemonState) {
	ss := st.Systemd
	switch {
	case ss == nil:
		return
	case !ss.Installed:
		fmt.Fprintln(w, "systemd service not installed (hoplet daemon install)")
		return
	case ss.State == "":
		fmt.Fprintf(w, "systemd service installed (%s) but not loaded\n", ss.Program)
	default:
		enabled := "enabled"
		if !ss.Enabled {
			enabled = "disabled"
		}
		fmt.Fprintf(w, "systemd service %s, %s, pid %d, last exit %s (%s)\n", ss.State, enabled, ss.PID, ss.LastExit, ss.Program)
	}
	if !ss.Linger {
		fmt.Fprintf(w, "lingering is off: the daemon stops when you log out (%s)\n", s.lingerHint())
	}
}

func (s systemdService) check(ctx context.Context) proto.Check {
	const name = "systemd"
	ss, err := s.m.Status(ctx)
	if err != nil {
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: err.Error()}
	}
	switch {
	case !ss.Installed:
		return proto.Check{Name: name, Status: proto.CheckWarn, Detail: "not installed; the daemon will not start at boot (hoplet daemon install)"}
	case !ss.Enabled || !ss.Running():
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: fmt.Sprintf("%s is installed but not running and enabled (state %q; hoplet daemon install)", ss.Unit, ss.State)}
	case otherBinary(ss.Program) != "":
		return proto.Check{Name: name, Status: proto.CheckWarn, Detail: fmt.Sprintf("runs %s, not this binary %s", ss.Program, otherBinary(ss.Program))}
	case !ss.Linger:
		return proto.Check{Name: name, Status: proto.CheckWarn, Detail: "lingering is off, so the daemon stops when you log out: " + s.lingerHint()}
	default:
		return proto.Check{Name: name, Status: proto.CheckOK, Detail: fmt.Sprintf("%s, pid %d", ss.State, ss.PID)}
	}
}

// otherBinary returns the running binary when program is another one; it
// is empty when they are the same or the running binary cannot be told.
func otherBinary(program string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" || program == exe || sameFile(program, exe) {
		return ""
	}
	return exe
}

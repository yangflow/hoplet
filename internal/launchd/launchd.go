// Package launchd installs the daemon as a macOS LaunchAgent: launchd
// starts it at login and restarts it when it exits abnormally.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Label is the LaunchAgent's label.
const Label = "io.github.yangflow.hoplet"

// Agent is what the LaunchAgent runs.
type Agent struct {
	Exe  string // absolute path of the hoplet binary
	Home string // Hoplet's home directory
}

// LogFile is the daemon's own rotated log; LaunchdLog catches what the
// process writes before its logger starts (crashes, flag errors).
func LogFile(home string) string    { return filepath.Join(home, "logs", "daemon.log") }
func launchdLog(home string) string { return filepath.Join(home, "logs", "launchd.log") }

// Plist renders the LaunchAgent property list.
func Plist(a Agent) []byte {
	var b bytes.Buffer
	str := func(s string) {
		b.WriteString("<string>")
		_ = xml.EscapeText(&b, []byte(s)) // a bytes.Buffer never fails
		b.WriteString("</string>")
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>`)
	str(Label)
	b.WriteString("\n\t<key>ProgramArguments</key>\n\t<array>")
	for _, arg := range []string{a.Exe, "daemon", "--home", a.Home, "--log", LogFile(a.Home)} {
		b.WriteString("\n\t\t")
		str(arg)
	}
	b.WriteString("\n\t</array>\n\t<key>RunAtLoad</key>\n\t<true/>\n")
	// Restarted unless it exited cleanly (a SIGTERM from launchctl, say).
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	for _, k := range []string{"StandardOutPath", "StandardErrorPath"} {
		b.WriteString("\t<key>" + k + "</key>\n\t")
		str(launchdLog(a.Home))
		b.WriteString("\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Manager installs and inspects the LaunchAgent of one user.
type Manager struct {
	UserHome string // the user's home directory
	UID      int
	Run      Runner // launchctl invocations

	// retryDelay is the wait before bootstrap is retried; zero means
	// defaultRetryDelay.
	retryDelay time.Duration
}

// defaultRetryDelay is how long Install waits before retrying a bootstrap
// that failed with "Input/output error", which launchd answers when the
// service it just booted out is not gone yet.
const defaultRetryDelay = 500 * time.Millisecond

// exitStatus returns the exit status of a launchctl that ran and failed.
func exitStatus(err error) (int, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() > 0 {
		return ee.ExitCode(), true
	}
	return 0, false
}

// notLoaded reports whether a failed bootout or print says the service is
// not loaded: exit status 3 or 113, or launchctl's wording.
func notLoaded(out []byte, err error) bool {
	if code, ok := exitStatus(err); ok && (code == 3 || code == 113) {
		return true
	}
	return bytes.Contains(out, []byte("No such process")) || bytes.Contains(out, []byte("Could not find"))
}

// bootout unloads the agent; it is not an error when none is loaded.
func (m Manager) bootout(ctx context.Context) error {
	out, err := m.Run(ctx, "launchctl", "bootout", m.target())
	if err == nil || (ctx.Err() == nil && notLoaded(out, err)) {
		return nil
	}
	return fmt.Errorf("launchd: bootout: %w: %s", err, strings.TrimSpace(string(out)))
}

func (m Manager) bootstrap(ctx context.Context) error {
	domain := "gui/" + strconv.Itoa(m.UID)
	out, err := m.Run(ctx, "launchctl", "bootstrap", domain, m.Path())
	if code, ok := exitStatus(err); ok && code == 5 {
		delay := m.retryDelay
		if delay == 0 {
			delay = defaultRetryDelay
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("launchd: bootstrap: %w", ctx.Err())
		}
		out, err = m.Run(ctx, "launchctl", "bootstrap", domain, m.Path())
	}
	if err != nil {
		return fmt.Errorf("launchd: bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Path is the LaunchAgent's plist file.
func (m Manager) Path() string {
	return filepath.Join(m.UserHome, "Library", "LaunchAgents", Label+".plist")
}

func (m Manager) target() string { return "gui/" + strconv.Itoa(m.UID) + "/" + Label }

// Install writes the plist and (re)loads it, replacing a loaded agent: a
// running daemon is stopped and the new one takes over its socket.
func (m Manager) Install(ctx context.Context, a Agent) error {
	if !filepath.IsAbs(a.Exe) || !filepath.IsAbs(a.Home) {
		return errors.New("launchd: the binary and home must be absolute paths")
	}
	if err := os.MkdirAll(filepath.Dir(LogFile(a.Home)), 0o700); err != nil {
		return fmt.Errorf("launchd: create log dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.Path()), 0o700); err != nil {
		return fmt.Errorf("launchd: create LaunchAgents: %w", err)
	}
	if err := writeFile(m.Path(), Plist(a)); err != nil {
		return err
	}
	// Nothing loaded is the normal first install; any other bootout
	// failure is reported. Bootstrap right after bootout can answer
	// "Input/output error" (5) and is retried once.
	if err := m.bootout(ctx); err != nil {
		return err
	}
	return m.bootstrap(ctx)
}

// Uninstall unloads the agent, which stops the daemon, and removes the
// plist. It is not an error if nothing is installed or loaded; a bootout
// that fails otherwise is returned and the plist stays.
func (m Manager) Uninstall(ctx context.Context) error {
	if err := m.bootout(ctx); err != nil {
		return err
	}
	if err := os.Remove(m.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("launchd: remove plist: %w", err)
	}
	return nil
}

// Status is what launchd knows about the agent.
type Status struct {
	Installed bool   `json:"installed"`
	Program   string `json:"program,omitempty"` // the binary the plist runs
	Loaded    bool   `json:"loaded"`
	State     string `json:"state,omitempty"` // running, not running, …
	PID       int    `json:"pid,omitempty"`
	LastExit  string `json:"last_exit,omitempty"`
}

var (
	programRe = regexp.MustCompile(`<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)
	stateRe   = regexp.MustCompile(`(?m)^\s*state = (.+)$`)
	pidRe     = regexp.MustCompile(`(?m)^\s*pid = (\d+)$`)
	exitRe    = regexp.MustCompile(`(?m)^\s*last exit code = (.+)$`)
)

// Status reads the plist and asks launchd about the agent.
func (m Manager) Status(ctx context.Context) (Status, error) {
	var s Status
	b, err := os.ReadFile(m.Path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Status{}, fmt.Errorf("launchd: read plist: %w", err)
	default:
		s.Installed = true
		if g := programRe.FindSubmatch(b); g != nil {
			s.Program = unescape(string(g[1]))
		}
	}
	out, err := m.Run(ctx, "launchctl", "print", m.target())
	if err != nil {
		if _, exited := exitStatus(err); exited && ctx.Err() == nil {
			return s, nil // launchctl ran and found no such service
		}
		return Status{}, fmt.Errorf("launchd: print: %w", err)
	}
	s.Loaded = true
	if g := stateRe.FindSubmatch(out); g != nil {
		s.State = strings.TrimSpace(string(g[1]))
	}
	if g := pidRe.FindSubmatch(out); g != nil {
		s.PID, _ = strconv.Atoi(string(g[1]))
	}
	if g := exitRe.FindSubmatch(out); g != nil {
		s.LastExit = strings.TrimSpace(string(g[1]))
	}
	return s, nil
}

func unescape(s string) string {
	var out string
	if err := xml.Unmarshal([]byte("<s>"+s+"</s>"), &out); err != nil {
		return s
	}
	return out
}

// writeFile replaces path with b (mode 0644, as launchd expects).
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hoplet-plist-*")
	if err != nil {
		return fmt.Errorf("launchd: write plist: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Chmod(0o644), tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("launchd: write plist: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("launchd: write plist: %w", err)
	}
	return nil
}

// Package systemd installs Hoplet's services on Linux: the daemon as a
// user service, started at boot when the user lingers and restarted when
// it exits abnormally, and the relay as a system service.
package systemd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DaemonUnit is the daemon's user service.
const DaemonUnit = "hoplet.service"

// RelayUnit is the relay's system service.
const RelayUnit = "hoplet-relay.service"

// RelayDir is the relay service's directory; systemd creates it for the
// service's dynamic user.
const RelayDir = "/var/lib/hoplet-relay"

// SystemUnits is where system services are installed.
const SystemUnits = "/etc/systemd/system"

// LingerDir holds a file for each user whose services run without a login
// session.
const LingerDir = "/var/lib/systemd/linger"

// Agent is what the daemon's service runs.
type Agent struct {
	Exe  string // absolute path of the hoplet binary
	Home string // Hoplet's home directory
}

// LogFile is the daemon's own rotated log; what it writes before its
// logger starts goes to the journal.
func LogFile(home string) string { return filepath.Join(home, "logs", "daemon.log") }

// Daemon renders the daemon's user unit.
func Daemon(a Agent) ([]byte, error) {
	exec, err := execLine(a.Exe, "daemon", "--home", a.Home, "--log", LogFile(a.Home))
	if err != nil {
		return nil, err
	}
	// The daemon restarts every 30 s while another holds the home's lock,
	// with no limit on how often. Stopping it ends only the daemon: shims
	// and harnesses outlive it by design.
	return []byte(`[Unit]
Description=Hoplet daemon
StartLimitIntervalSec=0

[Service]
ExecStart=` + exec + `
Restart=on-failure
RestartSec=30
KillMode=process

[Install]
WantedBy=default.target
`), nil
}

// Relay is what the relay's service runs.
type Relay struct {
	Exe     string   // absolute path of the hoplet binary
	Domains []string // the names it serves
	Listen  string   // host:port
}

// RelayService renders the relay's system unit. It runs as a dynamic user
// that may bind ports below 1024 and nothing else privileged.
func RelayService(r Relay) ([]byte, error) {
	if len(r.Domains) == 0 {
		return nil, errors.New("systemd: the relay needs a domain")
	}
	args := []string{r.Exe, "relay"}
	for _, d := range r.Domains {
		args = append(args, "--domain", d)
	}
	args = append(args, "--listen", r.Listen, "--dir", RelayDir)
	exec, err := execLine(args...)
	if err != nil {
		return nil, err
	}
	return []byte(`[Unit]
Description=Hoplet relay
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
ExecStart=` + exec + `
DynamicUser=yes
StateDirectory=hoplet-relay
StateDirectoryMode=0700
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`), nil
}

// execLine joins args into an ExecStart value. The binary must be an
// absolute path; control characters are not supported.
func execLine(args ...string) (string, error) {
	if !filepath.IsAbs(args[0]) {
		return "", errors.New("systemd: the binary must be an absolute path")
	}
	words := make([]string, len(args))
	for i, a := range args {
		for _, c := range a {
			if c < 0x20 || c == 0x7f {
				return "", fmt.Errorf("systemd: %q holds a control character, which a unit cannot carry", a)
			}
		}
		words[i] = quote(a)
	}
	return strings.Join(words, " "), nil
}

// quote writes one ExecStart word: % and $ doubled so that systemd does
// not expand them, and double quotes, with \ and " escaped, around a word
// that is empty or holds a space, a quote, a backslash or a semicolon.
func quote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if s != "" && !strings.ContainsAny(s, " \t\"'\\;") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// firstWord returns the first word of an ExecStart value as quote wrote it.
func firstWord(s string) string {
	var b strings.Builder
	if strings.HasPrefix(s, `"`) {
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				if i+1 < len(s) {
					i++
					b.WriteByte(s[i])
				}
			case '"':
				return unexpand(b.String())
			default:
				b.WriteByte(s[i])
			}
		}
		return unexpand(b.String())
	}
	w, _, _ := strings.Cut(s, " ")
	return unexpand(w)
}

func unexpand(s string) string { return strings.NewReplacer("%%", "%", "$$", "$").Replace(s) }

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Manager installs and inspects one unit.
type Manager struct {
	Dir  string // the directory the unit file is in
	Name string // the unit's name, such as hoplet.service
	User bool   // a user unit (systemctl --user) rather than a system one
	Run  Runner // systemctl invocations
	// Linger, when set, is the file whose presence means the user's
	// services keep running without a login session.
	Linger string
}

// Path is the unit file.
func (m Manager) Path() string { return filepath.Join(m.Dir, m.Name) }

func (m Manager) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	if m.User {
		args = append([]string{"--user"}, args...)
	}
	out, err := m.Run(ctx, "systemctl", args...)
	if err != nil {
		return out, fmt.Errorf("systemd: systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Install writes the unit, enables it and (re)starts it: a running service
// is stopped and the new one takes its place.
func (m Manager) Install(ctx context.Context, unit []byte) error {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("systemd: create %s: %w", m.Dir, err)
	}
	if err := writeFile(m.Path(), unit); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", m.Name}, {"restart", m.Name}} {
		if _, err := m.systemctl(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall stops and disables the unit and removes it. Without a unit
// file it does nothing; when systemctl fails the file stays.
func (m Manager) Uninstall(ctx context.Context) error {
	if _, err := os.Stat(m.Path()); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, err := m.systemctl(ctx, "disable", "--now", m.Name); err != nil {
		return err
	}
	if err := os.Remove(m.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("systemd: remove unit: %w", err)
	}
	_, err := m.systemctl(ctx, "daemon-reload")
	return err
}

// Status is what systemd knows about the unit.
type Status struct {
	Installed bool   `json:"installed"`
	Unit      string `json:"unit"`              // the unit file
	Program   string `json:"program,omitempty"` // the binary it runs
	Enabled   bool   `json:"enabled"`
	State     string `json:"state,omitempty"` // active (running), failed (failed), …
	PID       int    `json:"pid,omitempty"`
	LastExit  string `json:"last_exit,omitempty"`
	Linger    bool   `json:"linger"`
}

// Running reports whether systemd runs the unit's process.
func (s Status) Running() bool { return s.State == "active (running)" }

// Status reads the unit file and asks systemd about the unit.
func (m Manager) Status(ctx context.Context) (Status, error) {
	s := Status{Unit: m.Path()}
	if m.Linger != "" {
		_, err := os.Stat(m.Linger)
		s.Linger = err == nil
	}
	b, err := os.ReadFile(m.Path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Status{}, fmt.Errorf("systemd: read unit: %w", err)
	default:
		s.Installed = true
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "ExecStart="); ok {
				s.Program = firstWord(v)
				break
			}
		}
	}
	out, err := m.systemctl(ctx, "show", m.Name, "--property=LoadState,UnitFileState,ActiveState,SubState,MainPID,ExecMainStatus")
	if err != nil {
		return Status{}, err
	}
	props := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			props[k] = v
		}
	}
	if props["LoadState"] != "loaded" {
		return s, nil
	}
	s.Enabled = props["UnitFileState"] == "enabled"
	if a := props["ActiveState"]; a != "" {
		s.State = a + " (" + props["SubState"] + ")"
	}
	s.PID, _ = strconv.Atoi(props["MainPID"])
	s.LastExit = props["ExecMainStatus"]
	return s, nil
}

// writeFile replaces path with b, mode 0644, in one rename.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hoplet-unit-*")
	if err != nil {
		return fmt.Errorf("systemd: write unit: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Chmod(0o644), tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("systemd: write unit: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("systemd: write unit: %w", err)
	}
	return nil
}

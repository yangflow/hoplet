package systemd

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden unit files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs (go test -update to rewrite):\n%s", name, got)
	}
}

func TestDaemonUnit(t *testing.T) {
	b, err := Daemon(Agent{Exe: "/usr/local/bin/hoplet", Home: "/home/me/.hoplet"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "hoplet.service", b)
}

func TestRelayUnit(t *testing.T) {
	b, err := RelayService(Relay{Exe: "/usr/local/bin/hoplet", Domains: []string{"relay.example.com", "relay2.example.com"}, Listen: ":443"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "hoplet-relay.service", b)
	if _, err := RelayService(Relay{Exe: "/usr/local/bin/hoplet", Listen: ":443"}); err == nil {
		t.Error("a relay without a domain was rendered")
	}
}

func TestExecLineQuoting(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"/bin/hoplet", "daemon"}, "/bin/hoplet daemon"},
		{[]string{"/opt/my apps/hoplet", ""}, `"/opt/my apps/hoplet" ""`},
		{[]string{"/opt/a\"b\\c/hoplet"}, `"/opt/a\"b\\c/hoplet"`},
		{[]string{"/opt/100%/$HOME/hoplet"}, "/opt/100%%/$$HOME/hoplet"},
		{[]string{"/opt/x;y/it's"}, `"/opt/x;y/it's"`},
	} {
		got, err := execLine(tc.args...)
		if err != nil || got != tc.want {
			t.Errorf("%q: %q %v, want %q", tc.args, got, err, tc.want)
		}
		if p := firstWord(got); p != tc.args[0] {
			t.Errorf("%q: first word read back as %q", got, p)
		}
	}
	for _, args := range [][]string{{"hoplet"}, {"/bin/hoplet", "a\nb"}} {
		if _, err := execLine(args...); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

// recorder is a Runner that records commands and answers from a table of
// command prefixes.
type recorder struct {
	calls []string
	reply map[string]reply
}

type reply struct {
	out string
	err error
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, cmd)
	for prefix, rep := range r.reply {
		if strings.HasPrefix(cmd, prefix) {
			return []byte(rep.out), rep.err
		}
	}
	return nil, nil
}

func userManager(t *testing.T, r *recorder) Manager {
	return Manager{Dir: filepath.Join(t.TempDir(), "systemd", "user"), Name: DaemonUnit, User: true, Run: r.run, Linger: filepath.Join(t.TempDir(), "me")}
}

func TestInstallUninstall(t *testing.T) {
	r := &recorder{}
	m := userManager(t, r)
	unit, err := Daemon(Agent{Exe: "/usr/local/bin/hoplet", Home: "/home/me/.hoplet"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Install(context.Background(), unit); err != nil {
		t.Fatal(err)
	}
	want := "systemctl --user daemon-reload\nsystemctl --user enable hoplet.service\nsystemctl --user restart hoplet.service"
	if got := strings.Join(r.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s", got)
	}
	fi, err := os.Stat(m.Path())
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("unit %v %v", fi, err)
	}

	r.calls = nil
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "systemctl --user disable --now hoplet.service\nsystemctl --user daemon-reload"; strings.Join(r.calls, "\n") != want {
		t.Fatalf("calls:\n%s", strings.Join(r.calls, "\n"))
	}
	if _, err := os.Stat(m.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit left: %v", err)
	}
	r.calls = nil
	if err := m.Uninstall(context.Background()); err != nil || len(r.calls) != 0 {
		t.Fatalf("uninstall without a unit: %v %v", err, r.calls)
	}

	sys := Manager{Dir: t.TempDir(), Name: RelayUnit, Run: r.run}
	if err := sys.Install(context.Background(), unit); err != nil || r.calls[0] != "systemctl daemon-reload" {
		t.Fatalf("system unit: %v %v", err, r.calls)
	}
}

func TestFailuresAreReported(t *testing.T) {
	r := &recorder{reply: map[string]reply{"systemctl --user enable": {"Failed to connect to bus", errors.New("exit status 1")}}}
	m := userManager(t, r)
	err := m.Install(context.Background(), []byte("[Unit]\n"))
	if err == nil || !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Fatalf("install: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("went on after a failure: %v", r.calls)
	}
	r.reply = map[string]reply{"systemctl --user disable": {"Access denied", errors.New("exit status 1")}}
	if err := m.Uninstall(context.Background()); err == nil {
		t.Fatal("disable failure swallowed")
	}
	if _, err := os.Stat(m.Path()); err != nil {
		t.Fatalf("unit removed after a failure: %v", err)
	}
	if _, err := m.Status(context.Background()); err != nil {
		t.Fatalf("status: %v", err)
	}
	r.reply = map[string]reply{"systemctl --user show": {"Failed to connect to bus", errors.New("exit status 1")}}
	if _, err := m.Status(context.Background()); err == nil {
		t.Fatal("show failure swallowed")
	}
}

func TestStatus(t *testing.T) {
	r := &recorder{}
	m := userManager(t, r)
	r.reply = map[string]reply{"systemctl --user show": {"LoadState=not-found\nUnitFileState=\nActiveState=inactive\nSubState=dead\nMainPID=0\nExecMainStatus=0\n", nil}}
	s, err := m.Status(context.Background())
	if err != nil || s.Installed || s.Enabled || s.State != "" || s.Linger || s.Unit != m.Path() {
		t.Fatalf("not installed: %+v %v", s, err)
	}

	unit, err := Daemon(Agent{Exe: "/opt/my apps/hoplet", Home: "/home/me/.hoplet"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Install(context.Background(), unit); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Linger, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r.reply = map[string]reply{"systemctl --user show": {"LoadState=loaded\nUnitFileState=enabled\nActiveState=active\nSubState=running\nMainPID=4242\nExecMainStatus=0\n", nil}}
	s, err = m.Status(context.Background())
	want := Status{Installed: true, Unit: m.Path(), Program: "/opt/my apps/hoplet", Enabled: true, State: "active (running)", PID: 4242, LastExit: "0", Linger: true}
	if err != nil || s != want || !s.Running() {
		t.Fatalf("running: %+v %v", s, err)
	}

	r.reply = map[string]reply{"systemctl --user show": {"LoadState=loaded\nUnitFileState=disabled\nActiveState=failed\nSubState=failed\nMainPID=0\nExecMainStatus=1\n", nil}}
	s, err = m.Status(context.Background())
	if err != nil || s.Enabled || s.State != "failed (failed)" || s.PID != 0 || s.LastExit != "1" || s.Running() {
		t.Fatalf("failed: %+v %v", s, err)
	}
}

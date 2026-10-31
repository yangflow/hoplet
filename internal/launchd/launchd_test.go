package launchd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// recorder is a Runner that records commands and answers from a table.
type recorder struct {
	calls []string
	reply map[string]struct {
		out string
		err error
	}
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

func TestPlist(t *testing.T) {
	p := string(Plist(Agent{Exe: "/opt/hoplet & co/hoplet", Home: "/Users/me/.hoplet"}))
	for _, want := range []string{
		"<key>Label</key><string>io.github.yangflow.hoplet</string>",
		"<string>/opt/hoplet &amp; co/hoplet</string>",
		"<string>daemon</string>",
		"<string>--home</string>\n\t\t<string>/Users/me/.hoplet</string>",
		"<string>--log</string>\n\t\t<string>/Users/me/.hoplet/logs/daemon.log</string>",
		"<key>SuccessfulExit</key>\n\t\t<false/>",
		"<key>StandardErrorPath</key>\n\t<string>/Users/me/.hoplet/logs/launchd.log</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if runtime.GOOS == "darwin" {
		f := filepath.Join(t.TempDir(), "x.plist")
		if err := os.WriteFile(f, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %v: %s", err, out)
		}
	}
}

func TestInstallUninstall(t *testing.T) {
	user := t.TempDir()
	hoplet := filepath.Join(t.TempDir(), "home")
	r := &recorder{reply: map[string]struct {
		out string
		err error
	}{"launchctl bootout": {"Boot-out failed: 3: No such process", errors.New("exit status 3")}}}
	m := Manager{UserHome: user, UID: 501, Run: r.run}
	if err := m.Install(context.Background(), Agent{Exe: "/usr/local/bin/hoplet", Home: hoplet}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"launchctl bootout gui/501/io.github.yangflow.hoplet",
		"launchctl bootstrap gui/501 " + m.Path(),
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(r.calls, "\n"))
	}
	fi, err := os.Stat(m.Path())
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("plist %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(hoplet, "logs")); err != nil || !fi.IsDir() {
		t.Fatalf("log dir: %v", err)
	}
	s, err := m.Status(context.Background())
	if err != nil || !s.Installed || s.Program != "/usr/local/bin/hoplet" {
		t.Fatalf("status %+v %v", s, err)
	}

	r.calls = nil
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist left behind: %v", err)
	}
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
}

func TestInstallReportsBootstrapFailure(t *testing.T) {
	r := &recorder{reply: map[string]struct {
		out string
		err error
	}{"launchctl bootstrap": {"Bootstrap failed: 5: Input/output error\n", errors.New("exit status 5")}}}
	m := Manager{UserHome: t.TempDir(), UID: 501, Run: r.run}
	err := m.Install(context.Background(), Agent{Exe: "/bin/hoplet", Home: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("install: %v", err)
	}
	if err := m.Install(context.Background(), Agent{Exe: "hoplet", Home: "/h"}); err == nil {
		t.Fatal("relative binary accepted")
	}
}

func TestStatusParsesLaunchctl(t *testing.T) {
	r := &recorder{reply: map[string]struct {
		out string
		err error
	}{"launchctl print": {"gui/501/io.github.yangflow.hoplet = {\n\tactive count = 1\n\tstate = running\n\tprogram = /bin/hoplet\n\tpid = 4242\n\tlast exit code = 0\n}\n", nil}}}
	m := Manager{UserHome: t.TempDir(), UID: 501, Run: r.run}
	s, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Installed || !s.Loaded || s.State != "running" || s.PID != 4242 || s.LastExit != "0" {
		t.Fatalf("status %+v", s)
	}
	r.reply = map[string]struct {
		out string
		err error
	}{"launchctl print": {"Could not find service", errors.New("exit status 113")}}
	if s, err := m.Status(context.Background()); err != nil || s.Loaded {
		t.Fatalf("unloaded status %+v %v", s, err)
	}
}

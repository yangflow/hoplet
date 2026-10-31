package launchd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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

// exitErr returns the error of a command that exited with code.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("no exit error: %v", err)
	}
	return err
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
	}{"launchctl bootout": {"Boot-out failed: 3: No such process", exitErr(t, 3)}}}
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
	}{"launchctl bootstrap": {"Bootstrap failed: 5: Input/output error\n", exitErr(t, 5)}}}
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
	}{"launchctl print": {"Could not find service", exitErr(t, 113)}}
	if s, err := m.Status(context.Background()); err != nil || s.Loaded {
		t.Fatalf("unloaded status %+v %v", s, err)
	}
}

// scripted answers each launchctl call in turn.
type scripted struct {
	replies []struct {
		out string
		err error
	}
	calls []string
}

func (s *scripted) run(_ context.Context, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, name+" "+strings.Join(args, " "))
	if len(s.replies) == 0 {
		return nil, nil
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return []byte(r.out), r.err
}

func reply(out string, err error) struct {
	out string
	err error
} {
	return struct {
		out string
		err error
	}{out, err}
}

func TestInstallReturnsRealBootoutFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		out     string
		err     error
		ignored bool
	}{
		"exit 3":         {"", exitErr(t, 3), true},
		"exit 113":       {"", exitErr(t, 113), true},
		"no such":        {"Boot-out failed: No such process", exitErr(t, 1), true},
		"could not find": {"Could not find service", exitErr(t, 1), true},
		"permission":     {"Boot-out failed: 1: Operation not permitted", exitErr(t, 1), false},
		"exec missing":   {"", exec.ErrNotFound, false},
	} {
		s := &scripted{replies: []struct {
			out string
			err error
		}{reply(tc.out, tc.err)}}
		m := Manager{UserHome: t.TempDir(), UID: 501, Run: s.run}
		err := m.Install(context.Background(), Agent{Exe: "/bin/hoplet", Home: t.TempDir()})
		if tc.ignored && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !tc.ignored {
			if err == nil || !strings.Contains(err.Error(), "bootout") {
				t.Errorf("%s: %v", name, err)
			}
			if len(s.calls) != 1 {
				t.Errorf("%s: bootstrap ran after a failed bootout: %v", name, s.calls)
			}
		}
	}
}

func TestInstallRetriesBootstrapOnceOnIOError(t *testing.T) {
	s := &scripted{replies: []struct {
		out string
		err error
	}{reply("", nil), reply("Bootstrap failed: 5: Input/output error", exitErr(t, 5)), reply("", nil)}}
	m := Manager{UserHome: t.TempDir(), UID: 501, Run: s.run, retryDelay: time.Millisecond}
	if err := m.Install(context.Background(), Agent{Exe: "/bin/hoplet", Home: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if len(s.calls) != 3 || !strings.Contains(s.calls[2], "bootstrap") {
		t.Fatalf("calls %v", s.calls)
	}
	// Failing twice is reported; another failure code is not retried.
	s = &scripted{replies: []struct {
		out string
		err error
	}{reply("", nil), reply("x", exitErr(t, 5)), reply("y: Input/output error", exitErr(t, 5))}}
	m.Run = s.run
	if err := m.Install(context.Background(), Agent{Exe: "/bin/hoplet", Home: t.TempDir()}); err == nil || len(s.calls) != 3 {
		t.Fatalf("second failure: %v %v", err, s.calls)
	}
	s = &scripted{replies: []struct {
		out string
		err error
	}{reply("", nil), reply("x", exitErr(t, 1))}}
	m.Run = s.run
	if err := m.Install(context.Background(), Agent{Exe: "/bin/hoplet", Home: t.TempDir()}); err == nil || len(s.calls) != 2 {
		t.Fatalf("other failure: %v %v", err, s.calls)
	}
}

func TestUninstallKeepsThePlistOnARealBootoutFailure(t *testing.T) {
	m := Manager{UserHome: t.TempDir(), UID: 501}
	ok := &scripted{}
	m.Run = ok.run
	if err := m.Install(context.Background(), Agent{Exe: "/bin/hoplet", Home: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	bad := &scripted{replies: []struct {
		out string
		err error
	}{reply("Boot-out failed: 1: Operation not permitted", exitErr(t, 1))}}
	m.Run = bad.run
	if err := m.Uninstall(context.Background()); err == nil {
		t.Fatal("bootout failure swallowed")
	}
	if _, err := os.Stat(m.Path()); err != nil {
		t.Fatalf("plist removed: %v", err)
	}
	notLoaded := &scripted{replies: []struct {
		out string
		err error
	}{reply("", exitErr(t, 113))}}
	m.Run = notLoaded.run
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist left: %v", err)
	}
}

func TestStatusReturnsErrorsThatAreNotALaunchctlExit(t *testing.T) {
	s := &scripted{replies: []struct {
		out string
		err error
	}{reply("", exec.ErrNotFound)}}
	m := Manager{UserHome: t.TempDir(), UID: 501, Run: s.run}
	if _, err := m.Status(context.Background()); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("exec failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = &scripted{replies: []struct {
		out string
		err error
	}{reply("", exitErr(t, 1))}}
	m.Run = s.run
	if _, err := m.Status(ctx); err == nil {
		t.Fatal("ended context reported as not loaded")
	}
}

func TestStatusUnescapesTheProgramPath(t *testing.T) {
	s := &scripted{}
	m := Manager{UserHome: t.TempDir(), UID: 501, Run: s.run}
	exe := "/opt/a & b <c>/hoplet"
	if err := m.Install(context.Background(), Agent{Exe: exe, Home: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status(context.Background())
	if err != nil || st.Program != exe {
		t.Fatalf("program %q %v", st.Program, err)
	}
}

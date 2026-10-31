package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/launchd"
	"github.com/yangflow/hoplet/proto"
)

// cli runs one command line in this process against the test's home.
func cli(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = testApp(&out, &errb).run(args)
	return code, out.String(), errb.String()
}

// testApp is a command line whose LaunchAgent is the fake one.
func testApp(out, errb io.Writer) app {
	return app{stdout: out, stderr: errb, harnesses: map[string]harness.Harness{}, agents: fakeAgents, allowTempBinary: true}
}

// testHome makes a short temporary Hoplet home (Unix socket paths are
// limited) and a fake user home for the LaunchAgent, and points the
// environment at them.
func testHome(t *testing.T) (home, userHome string) {
	t.Helper()
	h, err := os.MkdirTemp("", "he")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(h) })
	t.Setenv("HOPLET_HOME", h)
	u := t.TempDir()
	t.Setenv("HOPLET_TEST_USERHOME", u)
	return h, u
}

func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := cli(t, args...)
	if code != 0 {
		t.Fatalf("hoplet %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

// startDaemonProcess runs `hoplet daemon` (this test binary) for home with
// the fake harness, and stops it when the test ends.
func startDaemonProcess(t *testing.T, home string, extraEnv ...string) {
	t.Helper()
	startDaemon(t, home, nil, extraEnv...)
}

// startDaemon is startDaemonProcess with extra command-line arguments.
func startDaemon(t *testing.T, home string, extraArgs []string, extraEnv ...string) {
	t.Helper()
	logf, err := os.Create(filepath.Join(home, "daemon.out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logf.Close() })
	cmd := exec.Command(os.Args[0], append([]string{"daemon", "--home", home}, extraArgs...)...)
	cmd.Env = append(os.Environ(), append([]string{"HOPLET_TEST_MAIN=1"}, extraEnv...)...)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("daemon exit: %v", err)
			}
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("the daemon did not stop on SIGTERM")
		}
		// Shims outlive the daemon by design; end them and their harnesses.
		_ = exec.Command("pkill", "-TERM", "-f", "shim --dir "+home).Run()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := waitServing(ctx, home)
	if err != nil {
		b, _ := os.ReadFile(filepath.Join(home, "daemon.out"))
		t.Fatalf("the daemon never served: %v\n%s", err, b)
	}
	c.Close()
}

// TestSessionInTheTerminal drives one session from the command line, end
// to end, against a real daemon process running the fake harness.
func TestSessionInTheTerminal(t *testing.T) {
	h, _ := testHome(t)
	work := filepath.Join(h, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeBin, err := fake.BuildBinary(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	script, err := fake.WriteScript(h, fake.Script{Session: "n1", Turns: []fake.Turn{
		{Steps: []fake.Step{
			{EchoPrompt: true},
			{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []fake.Option{
				{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "no", Label: "Deny", Scope: "deny"},
			}}},
			{AwaitApproval: "a1"},
		}},
		{Steps: []fake.Step{{SleepMS: 300}, {EchoPrompt: true}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	startDaemonProcess(t, h, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script, "HOPLET_TEST_PROBE="+probe.URL)

	id := strings.TrimSpace(mustCLI(t, "session", "new", "--harness", "fake", "--dir", work, "--title", "e2e", "first", "prompt"))
	if len(id) != 16 {
		t.Fatalf("session id %q", id)
	}
	short := id[:6]
	if out := mustCLI(t, "session", "wait", short, "--for", "blocked", "--timeout", "15s"); !strings.Contains(out, "approval a1: Run go test") {
		t.Fatalf("wait blocked: %q", out)
	}
	if code, _, errs := cli(t, "session", "approve", short); code != 2 || !strings.Contains(errs, "once (Allow once)") {
		t.Fatalf("approve without an option: exit %d, %q", code, errs)
	}
	if out := mustCLI(t, "session", "approve", short, "--option", "once"); out != "answered a1 with once\n" {
		t.Fatalf("approve: %q", out)
	}
	if code, _, errs := cli(t, "session", "approve", short, "--option", "once"); code != 1 || !strings.Contains(errs, "nothing waits") {
		t.Fatalf("approve again: exit %d, %q", code, errs)
	}
	mustCLI(t, "session", "wait", short, "--for", "idle", "--timeout", "15s")
	if code, _, errs := cli(t, "session", "wait", short, "--for", "blocked", "--timeout", "300ms"); code != 3 || !strings.Contains(errs, "timed out") {
		t.Fatalf("wait blocked on an idle session: exit %d, %q", code, errs)
	}
	if code, _, _ := cli(t, "session", "wait", short, "--timeout", "-1s"); code != 2 {
		t.Fatalf("negative timeout: exit %d", code)
	}
	if code, _, errs := cli(t, "session", "send", "abc", "x"); code != 1 || !strings.Contains(errs, "at least 4 characters") {
		t.Fatalf("short unknown ID: exit %d, %q", code, errs)
	}

	// wait right after send must not see the idle state before the turn.
	if out := mustCLI(t, "session", "send", short, "second"); out != "sent: turn 2\n" {
		t.Fatalf("send: %q", out)
	}
	mustCLI(t, "session", "wait", short, "--for", "idle", "--timeout", "15s")
	evs := watchAll(t, id)
	var texts []string
	for _, ev := range evs {
		if ev.Type == proto.TypeMessage && ev.Turn == 2 {
			p, _ := ev.Decode()
			texts = append(texts, p.(proto.Message).Text)
		}
	}
	if strings.Join(texts, "|") != "second|second" {
		t.Fatalf("turn 2 messages %q: wait returned before turn 2 ended", texts)
	}

	if out := mustCLI(t, "session", "list"); !strings.Contains(out, id) || !strings.Contains(out, "e2e") {
		t.Fatalf("list: %q", out)
	}
	var listed []proto.Session
	if err := json.Unmarshal([]byte(mustCLI(t, "session", "list", "--json")), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("list --json: %v %v", listed, err)
	}
	mustCLI(t, "session", "archive", short)
	if out := mustCLI(t, "session", "list"); strings.Contains(out, id) {
		t.Fatalf("archived session still listed: %q", out)
	}
	if out := mustCLI(t, "session", "list", "--all"); !strings.Contains(out, "archived") {
		t.Fatalf("list --all: %q", out)
	}
	if out := mustCLI(t, "session", "wait", short, "--timeout", "15s"); !strings.Contains(out, "archived") {
		t.Fatalf("wait done on an archived session: %q", out)
	}

	if out := mustCLI(t, "env", "refresh"); !strings.Contains(out, "3 variables") || !strings.Contains(out, "no proxy") {
		t.Fatalf("env refresh: %q", out)
	}
	if out := mustCLI(t, "daemon", "status"); !strings.Contains(out, "daemon dev serving") {
		t.Fatalf("daemon status: %q", out)
	}
	code, out, _ := cli(t, "doctor", "--json")
	var rep proto.DoctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out)
	}
	statuses := map[string]proto.CheckStatus{}
	for _, ch := range rep.Checks {
		statuses[ch.Name] = ch.Status
	}
	for _, name := range []string{"home", "token", "daemon", "environment", "network", "harness fake"} {
		if statuses[name] != proto.CheckOK {
			t.Errorf("doctor %s: %s\n%s", name, statuses[name], out)
		}
	}
	if code != 0 {
		t.Errorf("doctor exit %d with no failing check\n%s", code, out)
	}
}

// watchAll runs `hoplet session watch --json` as its own process, reads
// every event of the session and then interrupts it like Ctrl-C would.
func watchAll(t *testing.T, id string) []proto.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := c.Session(ctx, proto.SessionID(id))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "session", "watch", id, "--json")
	cmd.Env = append(os.Environ(), "HOPLET_TEST_MAIN=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var evs []proto.Event
	sc := bufio.NewScanner(stdout)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var ev proto.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("watch line %q: %v", sc.Text(), err)
		}
		evs = append(evs, ev)
		if ev.Seq == st.Session.LastSeq {
			break
		}
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch after SIGINT: %v\n%s", err, stderr.String())
	}
	if len(evs) == 0 || evs[len(evs)-1].Seq != st.Session.LastSeq {
		t.Fatalf("watch printed %d events, not up to seq %d", len(evs), st.Session.LastSeq)
	}
	return evs
}

// TestSeveralApprovals checks that approve needs an APPROVAL argument when
// more than one waits, and that it accepts one.
func TestSeveralApprovals(t *testing.T) {
	h, _ := testHome(t)
	work := filepath.Join(h, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeBin, err := fake.BuildBinary(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	opts := []fake.Option{{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "no", Label: "Deny", Scope: "deny"}}
	script, err := fake.WriteScript(h, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{EchoPrompt: true},
		{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "First", Options: opts}},
		{Emit: &fake.Ev{Ev: "approval", ID: "a2", Kind: "command", Title: "Second", Options: opts}},
		{AwaitApproval: "a1"},
		{AwaitApproval: "a2"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	startDaemonProcess(t, h, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script)
	id := strings.TrimSpace(mustCLI(t, "session", "new", "--harness", "fake", "--dir", work, "go"))
	short := id[:6]
	// Both approvals are emitted in one burst; poll until both are pending.
	for deadline := time.Now().Add(15 * time.Second); ; {
		out := mustCLI(t, "session", "wait", short, "--for", "blocked", "--timeout", "15s")
		if strings.Contains(out, "a1") && strings.Contains(out, "a2") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("approvals never both pending: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code, _, errs := cli(t, "session", "approve", short, "--option", "once"); code != 2 || !strings.Contains(errs, "a1") || !strings.Contains(errs, "a2") {
		t.Fatalf("approve with two pending: exit %d, %q", code, errs)
	}
	if out := mustCLI(t, "session", "approve", short, "a1", "--option", "once"); out != "answered a1 with once\n" {
		t.Fatalf("approve a1: %q", out)
	}
	if code, _, errs := cli(t, "session", "approve", short, "nope", "--option", "once"); code != 1 || !strings.Contains(errs, "no pending approval") {
		t.Fatalf("approve unknown: exit %d, %q", code, errs)
	}
	mustCLI(t, "session", "approve", short, "--option", "once")
	mustCLI(t, "session", "wait", short, "--for", "idle", "--timeout", "15s")
}

// TestDaemonAgent installs, inspects and removes the LaunchAgent against a
// running daemon, with launchctl replaced by a recorder.
func TestDaemonAgent(t *testing.T) {
	h, userHome := testHome(t)
	startDaemonProcess(t, h)
	plist := launchd.Manager{UserHome: userHome}.Path()
	launchAgent := func() proto.CheckStatus {
		_, out, _ := cli(t, "doctor", "--json")
		var rep proto.DoctorReport
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("doctor --json: %v\n%s", err, out)
		}
		for _, ch := range rep.Checks {
			if ch.Name == "launch agent" {
				return ch.Status
			}
		}
		return ""
	}
	if runtime.GOOS == "darwin" {
		if st := launchAgent(); st != proto.CheckWarn {
			t.Errorf("launch agent before install: %q", st)
		}
	}

	if out := mustCLI(t, "daemon", "install"); !strings.Contains(out, "serving") {
		t.Fatalf("install: %q", out)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("no plist under the fake user home: %v", err)
	}
	if !strings.Contains(string(b), "<array>\n\t\t<string>"+exe+"</string>") {
		t.Errorf("ProgramArguments[0] is not %s:\n%s", exe, b)
	}
	log, _ := os.ReadFile(filepath.Join(userHome, "launchctl.log"))
	if !strings.Contains(string(log), "launchctl bootout") || !strings.Contains(string(log), "launchctl bootstrap") {
		t.Errorf("launchctl calls:\n%s", log)
	}
	if out := mustCLI(t, "daemon", "status"); !strings.Contains(out, "LaunchAgent running") {
		t.Errorf("status: %q", out)
	}
	if runtime.GOOS == "darwin" {
		if st := launchAgent(); st != proto.CheckOK {
			t.Errorf("launch agent after install: %q", st)
		}
	}
	var inst struct {
		Plist   string `json:"plist"`
		Serving bool   `json:"serving"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, "daemon", "install", "--json")), &inst); err != nil || inst.Plist != plist || !inst.Serving {
		t.Errorf("install --json: %+v %v", inst, err)
	}

	if out := mustCLI(t, "daemon", "uninstall"); !strings.Contains(out, "uninstalled") {
		t.Fatalf("uninstall: %q", out)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Errorf("plist after uninstall: %v", err)
	}
	if out := mustCLI(t, "daemon", "status"); !strings.Contains(out, "not installed") {
		t.Errorf("status after uninstall: %q", out)
	}
}

func TestDaemonLogFile(t *testing.T) {
	h, _ := testHome(t)
	logPath := filepath.Join(h, "daemon.log")
	startDaemon(t, h, []string{"--log", logPath})
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if line, _, _ := strings.Cut(string(b), "\n"); line != "" {
			if !json.Valid([]byte(line)) {
				t.Fatalf("log line %q is not JSON", line)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing logged to %s", logPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

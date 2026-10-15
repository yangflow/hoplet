package runtime_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/runtime/claude"
	"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	runtimetest.Cleanup()
	os.Exit(code)
}

const wait = 10 * time.Second

type recorder struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []runtime.Event
}

func newRecorder() *recorder {
	r := &recorder{}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *recorder) emit(ev runtime.Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	r.cond.Broadcast()
}

// states is the sequence of states reported for a session.
func (r *recorder) states(id string) []runtime.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runtime.State
	for _, ev := range r.events {
		if s, ok := ev.(runtime.StateEvent); ok && s.SessionID == id {
			out = append(out, s.State)
		}
	}
	return out
}

func (r *recorder) outputs(id string) []runtime.Output {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runtime.Output
	for _, ev := range r.events {
		if o, ok := ev.(runtime.OutputEvent); ok && o.SessionID == id {
			out = append(out, o.Output)
		}
	}
	return out
}

func (r *recorder) permissions(id string) []runtime.PermissionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runtime.PermissionRequest
	for _, ev := range r.events {
		if p, ok := ev.(runtime.PermissionEvent); ok && p.SessionID == id {
			out = append(out, p.Request)
		}
	}
	return out
}

func (r *recorder) lastState(id string) (runtime.StateEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var last runtime.StateEvent
	found := false
	for _, ev := range r.events {
		if s, ok := ev.(runtime.StateEvent); ok && s.SessionID == id {
			last, found = s, true
		}
	}
	return last, found
}

// waitFor blocks until cond holds over the recorder's events.
func (r *recorder) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	timer := time.AfterFunc(wait, r.cond.Broadcast)
	defer timer.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		r.mu.Unlock()
		ok := cond()
		r.mu.Lock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			// The deferred Unlock runs as Fatalf exits the goroutine; unlocking here as well is a fatal error that
			// hides the timeout. The mutex is held, so the events can be read.
			t.Fatalf("timed out waiting for %s; events so far: %+v", what, r.events)
		}
		r.cond.Wait()
	}
}

func (r *recorder) waitState(t *testing.T, id string, want runtime.State) {
	t.Helper()
	r.waitFor(t, string(want)+" of "+id, func() bool {
		s, ok := r.lastState(id)
		return ok && s.State == want
	})
}

type env struct {
	t        *testing.T
	m        *runtime.Manager
	rec      *recorder
	projects string
	workdir  string
	log      string
	files    map[string]string
}

func newEnv(t *testing.T, tune func(*runtime.Config)) *env {
	t.Helper()
	bin := runtimetest.Build(t)
	e := &env{t: t, rec: newRecorder(), projects: t.TempDir(), files: map[string]string{}}
	e.workdir = t.TempDir()
	e.log = filepath.Join(t.TempDir(), "starts.jsonl")
	t.Setenv("FAKE_CLAUDE_PROJECTS", e.projects)
	t.Setenv("FAKE_CLAUDE_LOG", e.log)
	cfg := runtime.Config{
		Driver: claude.New(claude.Options{Bin: bin}),
		Locate: func(id string) (runtime.Location, error) {
			p, ok := e.files[id]
			if !ok {
				return runtime.Location{}, runtime.ErrSessionNotFound
			}
			return runtime.Location{Dir: e.workdir, File: p}, nil
		},
		Emit:              e.rec.emit,
		KillAfter:         500 * time.Millisecond,
		PermissionTimeout: time.Minute,
		ExternalQuiet:     300 * time.Millisecond,
		MaxIdle:           10,
	}
	if tune != nil {
		tune(&cfg)
	}
	e.m = runtime.NewManager(cfg)
	t.Cleanup(func() {
		ctx, cancel := contextTimeout(5 * time.Second)
		defer cancel()
		e.m.Shutdown(ctx)
	})
	return e
}

func (e *env) session(id string) string {
	e.t.Helper()
	p := runtimetest.Session(e.t, e.projects, e.workdir, id)
	e.files[id] = p
	return p
}

// starts returns the process starts logged by the fake.
func (e *env) starts() []struct {
	PID  int      `json:"pid"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
} {
	type start = struct {
		PID  int      `json:"pid"`
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
	}
	var out []start
	f, err := os.Open(e.log)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s start
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func sameStates(got, want []runtime.State) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func (e *env) expectStates(id string, want ...runtime.State) {
	e.t.Helper()
	if got := e.rec.states(id); !sameStates(got, want) {
		e.t.Fatalf("states of %s = %v, want %v", id, got, want)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func TestPromptRunsATurn(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	before := fileSize(t, file)
	if err := e.m.Prompt("s1", "say hello there"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready after the turn", func() bool {
		s := e.rec.states("s1")
		return len(s) >= 3 && s[len(s)-1] == runtime.StateReady
	})
	e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateReady)

	last, _ := e.rec.lastState("s1")
	if len(last.Capabilities) != 6 {
		t.Fatalf("capabilities = %v", last.Capabilities)
	}
	var text string
	var thinking int
	for _, o := range e.rec.outputs("s1") {
		switch o.Kind {
		case runtime.OutputText:
			text += o.Text
		case runtime.OutputThinking:
			thinking += o.Tokens
			if o.Text != "" {
				t.Fatalf("thinking carries text %q", o.Text)
			}
		}
		if strings.Contains(o.Text, "SIGNATURE") {
			t.Fatal("signature_delta was relayed")
		}
	}
	if text != "hello there" || thinking != 50 {
		t.Fatalf("text %q thinking %d", text, thinking)
	}
	if fileSize(t, file) <= before {
		t.Fatal("the turn was not written to the session file")
	}

	starts := e.starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %d", len(starts))
	}
	// Thinking is explicit, on unless the session chose it off.
	want := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool", "stdio", "--thinking", "enabled", "--resume", "s1"}
	if strings.Join(starts[0].Args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v", starts[0].Args)
	}
	if real, _ := filepath.EvalSymlinks(e.workdir); starts[0].Cwd != real && starts[0].Cwd != e.workdir {
		t.Fatalf("cwd = %q, want %q", starts[0].Cwd, e.workdir)
	}

	// The next prompt reuses the process: ready -> running -> ready, no second start.
	if err := e.m.Prompt("s1", "say again"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "second turn", func() bool { return len(e.rec.states("s1")) >= 5 })
	e.rec.waitState(t, "s1", runtime.StateReady)
	if n := len(e.starts()); n != 1 {
		t.Fatalf("starts after the second prompt = %d", n)
	}
}

func TestPromptImagesReachTheVendorBeforeTheText(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	png := []byte("\x89PNG\r\n\x1a\n-png")
	jpeg := []byte("\xff\xd8\xff-jpeg")
	err := e.m.PromptWith("s1", runtime.Prompt{Text: "images", Images: []runtime.Image{
		{MediaType: "image/png", Data: png}, {MediaType: "image/jpeg", Data: jpeg},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready after the turn", func() bool {
		s := e.rec.states("s1")
		return len(s) >= 3 && s[len(s)-1] == runtime.StateReady
	})
	var text string
	for _, o := range e.rec.outputs("s1") {
		if o.Kind == runtime.OutputText {
			text += o.Text
		}
	}
	if text != "2 images: image/png,image/jpeg" {
		t.Fatalf("the vendor saw %q", text)
	}
	// The vendor records the images in the user line, as base64 image content.
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("the user line does not carry the image")
	}
}

// textOf joins the streamed text of a session's outputs from index from on.
func textOf(e *env, sessionID string, from int) string {
	var text string
	for _, o := range e.rec.outputs(sessionID)[from:] {
		if o.Kind == runtime.OutputText {
			text += o.Text
		}
	}
	return text
}

func TestThePromptChangesTheOptionsFromItsTurnOn(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	off, on := false, true
	turn := func(p runtime.Prompt) string {
		t.Helper()
		from := len(e.rec.outputs("s1"))
		n := len(e.rec.states("s1"))
		if err := e.m.PromptWith("s1", p); err != nil {
			t.Fatal(err)
		}
		e.rec.waitFor(t, "the turn", func() bool {
			s := e.rec.states("s1")
			return len(s) > n+1 && s[len(s)-1] == runtime.StateReady
		})
		return textOf(e, "s1", from)
	}
	args := func(i int) string { return strings.Join(e.starts()[i].Args, " ") }

	// A new process starts with the prompt's options.
	if got := turn(runtime.Prompt{Text: "model", Options: runtime.Change{Model: "sonnet"}}); got != "model: sonnet" {
		t.Fatalf("first turn ran on %q", got)
	}
	if last, _ := e.rec.lastState("s1"); last.Options.Model != "sonnet" || last.Options.Thinking == nil || !*last.Options.Thinking {
		t.Fatalf("state reports %+v", last.Options)
	}
	// The model switches in place; a prompt that changes nothing keeps it.
	if got := turn(runtime.Prompt{Text: "model", Options: runtime.Change{Model: "opus"}}); got != "model: opus" {
		t.Fatalf("switched turn ran on %q", got)
	}
	if got := turn(runtime.Prompt{Text: "model"}); got != "model: opus" {
		t.Fatalf("next turn ran on %q", got)
	}
	if n := len(e.starts()); n != 1 {
		t.Fatalf("a model switch restarted the process: %d starts", n)
	}
	// Thinking and Fast restart the process between turns, with every option as it now stands.
	if got := turn(runtime.Prompt{Text: "model", Options: runtime.Change{Thinking: &off, Fast: &on}}); got != "model: opus" {
		t.Fatalf("restarted turn ran on %q", got)
	}
	if n := len(e.starts()); n != 2 {
		t.Fatalf("starts = %d, want a restart", n)
	}
	if a := args(1); !strings.Contains(a, "--model opus --thinking disabled --settings") || !strings.Contains(a, "--resume s1") {
		t.Fatalf("restart args = %s", a)
	}
	states := e.rec.states("s1")
	if states[len(states)-3] != runtime.StateStarting {
		t.Fatalf("the restart is not reported as starting: %v", states)
	}
	// An option the driver does not declare is refused before anything is written.
	if err := e.m.PromptWith("s1", runtime.Prompt{Text: "x", Options: runtime.Change{Model: "gpt-9"}}); !errors.Is(err, runtime.ErrOptionNotDeclared) {
		t.Fatalf("undeclared model: %v", err)
	}
}

func TestBusyAndStartingAreRefused(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_INIT_DELAY_MS", "400")
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "sleep"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Prompt("s1", "say no"); !errors.Is(err, runtime.ErrStarting) {
		t.Fatalf("prompt while starting: %v", err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	if err := e.m.Prompt("s1", "say no"); !errors.Is(err, runtime.ErrBusy) {
		t.Fatalf("prompt while running: %v", err)
	}
	if err := e.m.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
}

func TestUnknownSession(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.m.Prompt("nope", "say x"); !errors.Is(err, runtime.ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPermissionAllow(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "tool touch /tmp/x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateWaitingPermission)
	e.rec.waitFor(t, "permission request", func() bool { return len(e.rec.permissions("s1")) == 1 })
	reqs := e.rec.permissions("s1")
	if len(reqs) != 1 || reqs[0].Tool != "Bash" || !strings.Contains(string(reqs[0].Input), "touch /tmp/x") {
		t.Fatalf("requests = %+v", reqs)
	}
	if err := e.m.Permission("s1", reqs[0].RequestID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Permission("s1", reqs[0].RequestID, false); !errors.Is(err, runtime.ErrUnknownRequest) {
		t.Fatalf("second answer: %v", err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateWaitingPermission, runtime.StateRunning, runtime.StateReady)
	var call, result *runtime.Output
	for _, o := range e.rec.outputs("s1") {
		o := o
		switch o.Kind {
		case runtime.OutputToolCall:
			call = &o
		case runtime.OutputToolResult:
			result = &o
		}
	}
	if call == nil || result == nil || call.ToolUseID != result.ToolUseID || result.IsError || result.Text != "ran: touch /tmp/x" {
		t.Fatalf("call %+v result %+v", call, result)
	}
}

func TestPermissionDenyContinuesTheTurn(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "tool rm -rf /"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateWaitingPermission)
	e.rec.waitFor(t, "permission request", func() bool { return len(e.rec.permissions("s1")) == 1 })
	if err := e.m.Permission("s1", e.rec.permissions("s1")[0].RequestID, false); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	var text string
	var denied bool
	for _, o := range e.rec.outputs("s1") {
		if o.Kind == runtime.OutputText {
			text += o.Text
		}
		if o.Kind == runtime.OutputToolResult && o.IsError {
			denied = true
		}
	}
	if text != "REFUSED" || !denied {
		t.Fatalf("text %q denied %v", text, denied)
	}
	// The process accepted the answer and serves the next prompt.
	if err := e.m.Prompt("s1", "say still here"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "second turn", func() bool { return len(e.rec.states("s1")) >= 7 })
	if n := len(e.starts()); n != 1 {
		t.Fatalf("starts = %d", n)
	}
}

func TestPermissionTimeoutDenies(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.PermissionTimeout = 300 * time.Millisecond })
	e.session("s1")
	if err := e.m.Prompt("s1", "tool echo hi"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateWaitingPermission)
	e.rec.waitFor(t, "permission request", func() bool { return len(e.rec.permissions("s1")) == 1 })
	id := e.rec.permissions("s1")[0].RequestID
	e.rec.waitState(t, "s1", runtime.StateReady)
	if err := e.m.Permission("s1", id, true); !errors.Is(err, runtime.ErrUnknownRequest) {
		t.Fatalf("answer after the timeout: %v", err)
	}
	var denied bool
	for _, o := range e.rec.outputs("s1") {
		if o.Kind == runtime.OutputToolResult && o.IsError && strings.Contains(o.Text, "denied by Hoplet") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("the tool call was not denied: %+v", e.rec.outputs("s1"))
	}
}

func TestInterruptKeepsTheProcess(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Interrupt("s1"); !errors.Is(err, runtime.ErrNotRunning) {
		t.Fatalf("interrupt without a process: %v", err)
	}
	if err := e.m.Prompt("s1", "sleep"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	if err := e.m.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateReady)

	// Interrupt during a pending permission request ends the turn the same way.
	if err := e.m.Prompt("s1", "tool echo hi"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateWaitingPermission)
	if err := e.m.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready again", func() bool {
		s := e.rec.states("s1")
		return len(s) >= 6 && s[len(s)-1] == runtime.StateReady
	})
	if got := e.m.Pending(); len(got) != 0 {
		t.Fatalf("pending after interrupt: %v", got)
	}
	if n := len(e.starts()); n != 1 {
		t.Fatalf("interrupt restarted the process: %d starts", n)
	}
	for _, s := range e.rec.states("s1") {
		if s == runtime.StateFailed {
			t.Fatal("an interrupted turn is not a failure")
		}
	}
}

func TestFailedTurnAllowsRetry(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "fail"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateFailed)
	last, _ := e.rec.lastState("s1")
	if !strings.Contains(last.Error, "boom") {
		t.Fatalf("error = %q", last.Error)
	}
	if err := e.m.Prompt("s1", "say recovered"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	if n := len(e.starts()); n != 1 {
		t.Fatalf("starts = %d", n)
	}
}

func TestProcessCrashIsFailedAndRestarts(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "crash"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateFailed)
	last, _ := e.rec.lastState("s1")
	if !strings.Contains(last.Error, "exited") || !strings.Contains(last.Error, "crashed") {
		t.Fatalf("error = %q", last.Error)
	}
	if err := e.m.Prompt("s1", "say back"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	if n := len(e.starts()); n != 2 {
		t.Fatalf("starts = %d", n)
	}
}

func TestStartFailure(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_EXIT_ON_START", "1")
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateFailed)
	if last, _ := e.rec.lastState("s1"); !strings.Contains(last.Error, "exiting on start") {
		t.Fatalf("error = %q", last.Error)
	}
}

func TestUnsupportedVersionIsRefused(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_VERSION", "2.1.200")
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateFailed)
	last, _ := e.rec.lastState("s1")
	if !strings.Contains(last.Error, "unsupported Claude Code version") || !strings.Contains(last.Error, "2.1.200") || !strings.Contains(last.Error, "2.1.289") {
		t.Fatalf("error = %q", last.Error)
	}
	if n := len(e.starts()); n != 0 {
		t.Fatalf("a refused version started %d processes", n)
	}
}

func TestNewerVersionRunsAndIsUntested(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_VERSION", "2.1.300")
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
}

func TestStopEndsTheProcess(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	e.m.Stop("s1")
	e.rec.waitState(t, "s1", runtime.StateIdle)
	if got := e.m.States(); len(got) != 0 {
		t.Fatalf("States after stop = %+v", got)
	}
}

func TestStopKillsAProcessThatIgnoresClosedInput(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_IGNORE_STDIN_CLOSE", "1")
	e := newEnv(t, func(c *runtime.Config) { c.KillAfter = 300 * time.Millisecond })
	e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	start := time.Now()
	e.m.Stop("s1")
	e.rec.waitState(t, "s1", runtime.StateIdle)
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Fatalf("the process was killed after %v, before KillAfter", d)
	}
}

func TestStopWhileRunningEndsTheTurn(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "sleep"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	e.m.Stop("s1")
	e.rec.waitState(t, "s1", runtime.StateIdle)
}

func TestIdleProcessStopsAfterTheIdleTimeout(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.IdleTimeout = 300 * time.Millisecond })
	e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	e.rec.waitFor(t, "idle", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateIdle
	})
	// The next prompt starts a new process.
	if err := e.m.Prompt("s1", "say y"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "second start", func() bool { return len(e.starts()) == 2 })
}

func TestTurnsAreNotLimitedButIdleProcessesAre(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.MaxIdle = 2 })
	ids := []string{"a", "b", "c", "d"}
	for _, id := range ids {
		e.session(id)
		if err := e.m.Prompt(id, "sleep"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		e.rec.waitState(t, id, runtime.StateRunning) // four turns in parallel
	}
	// End the turns one after another: a, b, c, d become ready in this order.
	for _, id := range ids {
		if err := e.m.Interrupt(id); err != nil {
			t.Fatal(err)
		}
		e.rec.waitFor(t, id+" ready", func() bool {
			s, _ := e.rec.lastState(id)
			return s.State == runtime.StateReady || s.State == runtime.StateIdle
		})
		time.Sleep(20 * time.Millisecond)
	}
	// With two idle processes allowed, a and b (least recently used) were stopped.
	e.rec.waitState(t, "a", runtime.StateIdle)
	e.rec.waitState(t, "b", runtime.StateIdle)
	for _, id := range []string{"c", "d"} {
		if s, _ := e.rec.lastState(id); s.State != runtime.StateReady {
			t.Fatalf("%s = %s, want ready", id, s.State)
		}
	}
	if got := e.m.States(); len(got) != 2 {
		t.Fatalf("States = %+v", got)
	}
}

func TestUseRefreshesTheLeastRecentlyUsedOrder(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.MaxIdle = 2 })
	for _, id := range []string{"a", "b"} {
		e.session(id)
		if err := e.m.Prompt(id, "say x"); err != nil {
			t.Fatal(err)
		}
		e.rec.waitState(t, id, runtime.StateReady)
		time.Sleep(20 * time.Millisecond)
	}
	// a runs another turn, so b is now the least recently used.
	if err := e.m.Prompt("a", "say again"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "a ready again", func() bool { return len(e.rec.states("a")) >= 5 })
	time.Sleep(20 * time.Millisecond)
	e.session("c")
	if err := e.m.Prompt("c", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "c", runtime.StateReady)
	e.rec.waitState(t, "b", runtime.StateIdle)
	if s, _ := e.rec.lastState("a"); s.State != runtime.StateReady {
		t.Fatalf("a = %s", s.State)
	}
}

func TestOwnWritesAreNotExternal(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	e.m.Observe("s1", fileSize(t, file))
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.m.Observe("s1", fileSize(t, file)) // during the turn: ignored
	e.rec.waitState(t, "s1", runtime.StateReady)
	e.m.Observe("s1", fileSize(t, file))
	if got := e.m.State("s1"); got != runtime.StateReady {
		t.Fatalf("state = %s", got)
	}
}

func appendForeign(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"from a terminal\"}}\n"); err != nil {
		t.Fatal(err)
	}
}

func TestGrowthBeyondTheRecordedSizeIsExternal(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)

	appendForeign(t, file)
	e.m.Observe("s1", fileSize(t, file))
	e.rec.waitState(t, "s1", runtime.StateExternal)
	if err := e.m.Prompt("s1", "say no"); !errors.Is(err, runtime.ErrExternal) {
		t.Fatalf("prompt while external: %v", err)
	}
	// The process was stopped: its memory no longer matches the file.
	e.rec.waitFor(t, "process exit", func() bool { return e.m.State("s1") == runtime.StateExternal })
	time.Sleep(200 * time.Millisecond)
	if e.m.State("s1") != runtime.StateExternal {
		t.Fatalf("state = %s", e.m.State("s1"))
	}

	// Growth restarts the quiet period.
	time.Sleep(200 * time.Millisecond)
	appendForeign(t, file)
	e.m.Observe("s1", fileSize(t, file))
	time.Sleep(200 * time.Millisecond)
	e.m.Observe("s1", fileSize(t, file))
	if e.m.State("s1") != runtime.StateExternal {
		t.Fatalf("state after growth = %s", e.m.State("s1"))
	}
	// Quiet for ExternalQuiet: back to idle, and a prompt starts a process again.
	time.Sleep(350 * time.Millisecond)
	e.m.Observe("s1", fileSize(t, file))
	e.rec.waitState(t, "s1", runtime.StateIdle)
	if err := e.m.Prompt("s1", "say back"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "second process", func() bool { return len(e.starts()) == 2 })
}

func TestGrowthWithoutAProcessIsExternal(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	e.m.Observe("s1", fileSize(t, file)) // the first observation is the baseline
	if got := e.m.State("s1"); got != runtime.StateIdle {
		t.Fatalf("state = %s", got)
	}
	appendForeign(t, file)
	e.m.Observe("s1", fileSize(t, file))
	e.rec.waitState(t, "s1", runtime.StateExternal)
	if err := e.m.Prompt("s1", "say no"); !errors.Is(err, runtime.ErrExternal) {
		t.Fatalf("prompt: %v", err)
	}
	if n := len(e.starts()); n != 0 {
		t.Fatalf("starts = %d", n)
	}
}

func TestPromptChecksTheFileItself(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	appendForeign(t, file) // no scan has seen it yet
	if err := e.m.Prompt("s1", "say no"); !errors.Is(err, runtime.ErrExternal) {
		t.Fatalf("prompt: %v", err)
	}
	if got := e.m.State("s1"); got != runtime.StateExternal {
		t.Fatalf("state = %s", got)
	}
}

func TestStatesReportsRunningProcesses(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	e.session("s2")
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	if err := e.m.Prompt("s2", "sleep"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s2", runtime.StateRunning)
	got := e.m.States()
	if len(got) != 2 || got[0].SessionID != "s1" || got[0].State != runtime.StateReady || got[1].State != runtime.StateRunning {
		t.Fatalf("States = %+v", got)
	}
	if len(got[0].Capabilities) != 6 {
		t.Fatalf("capabilities = %v", got[0].Capabilities)
	}
}

func contextTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func newSessionFile(t *testing.T, e *env, id string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(e.projects, "*", id+".jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files of %s = %v", id, files)
	}
	return files[0]
}

func TestCreateStartsANewSessionWithoutResuming(t *testing.T) {
	e := newEnv(t, nil)
	on := true
	id, err := e.m.Create(e.workdir, "say hello", runtime.StartOptions{Model: "opus", Thinking: &on, Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready after the first turn", func() bool {
		s := e.rec.states(id)
		return len(s) >= 3 && s[len(s)-1] == runtime.StateReady
	})
	e.expectStates(id, runtime.StateStarting, runtime.StateRunning, runtime.StateReady)

	starts := e.starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %v", starts)
	}
	args := strings.Join(starts[0].Args, " ")
	for _, want := range []string{"--session-id " + id, "--model opus", "--thinking enabled", `--settings {"fastMode":true}`} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q lack %q", args, want)
		}
	}
	if strings.Contains(args, "--resume") {
		t.Fatalf("a new session resumes nothing: %q", args)
	}
	last, _ := e.rec.lastState(id)
	if len(last.Models) != 3 {
		t.Fatalf("models = %v", last.Models)
	}

	// The prompt is in the file Claude Code created, and the session is driven like any other.
	file := newSessionFile(t, e, id)
	raw, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(raw), "say hello") {
		t.Fatalf("file = %q, %v", raw, err)
	}
	e.files[id] = file
	if err := e.m.Prompt(id, "say again"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "second turn", func() bool {
		s := e.rec.states(id)
		return len(s) >= 5 && s[len(s)-1] == runtime.StateReady
	})
	if n := len(e.starts()); n != 1 {
		t.Fatalf("the second prompt started %d processes", n)
	}
}

func TestCreateRefusesOptionsTheDriverDoesNotDeclare(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.m.Create(e.workdir, "say x", runtime.StartOptions{Model: "gpt-9"}); !errors.Is(err, runtime.ErrOptionNotDeclared) {
		t.Fatalf("model: %v", err)
	}
	if n := len(e.starts()); n != 0 {
		t.Fatalf("a refused session started %d processes", n)
	}
}

func TestNewSessionWhoseProcessExitsFailsWithoutAFile(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_EXIT_ON_START", "1")
	e := newEnv(t, nil)
	id, err := e.m.Create(e.workdir, "say x", runtime.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, id, runtime.StateFailed)
	if state, msg := e.m.Info(id); state != runtime.StateFailed || msg == "" {
		t.Fatalf("info = %s %q", state, msg)
	}
	if files, _ := filepath.Glob(filepath.Join(e.projects, "*", id+".jsonl")); len(files) != 0 {
		t.Fatalf("files = %v", files)
	}
	if !e.m.Discard(id, wait) {
		t.Fatal("the process is still there")
	}
	for _, st := range e.m.States() {
		if st.SessionID == id {
			t.Fatalf("a discarded session is still reported: %+v", st)
		}
	}
}

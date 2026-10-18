package runtime_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	// Claude Code reports its state only when asked to; the driver must ask, whatever the agent inherits.
	t.Setenv("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS", "")
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
		OwnGrace:          50 * time.Millisecond,
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

func TestATurnEndsInReadyWhateverTheAgentsEnvironment(t *testing.T) {
	for _, inherited := range []string{"", "0"} {
		t.Run("inherited="+inherited, func(t *testing.T) {
			e := newEnv(t, nil)
			t.Setenv("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS", inherited)
			e.session("s1")
			if err := e.m.Prompt("s1", "say hello there"); err != nil {
				t.Fatal(err)
			}
			e.rec.waitFor(t, "ready after the turn", func() bool {
				s := e.rec.states("s1")
				return len(s) >= 3 && s[len(s)-1] == runtime.StateReady
			})
			e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateReady)
		})
	}
}

func TestTurnStartIsCalledForEveryAcceptedPrompt(t *testing.T) {
	var mu sync.Mutex
	var started []string
	e := newEnv(t, func(c *runtime.Config) {
		c.TurnStart = func(id string) { mu.Lock(); started = append(started, id); mu.Unlock() }
	})
	e.session("s1")
	if err := e.m.Prompt("s1", "say hello there"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	mu.Lock()
	n := len(started)
	mu.Unlock()
	if n == 0 || started[0] != "s1" {
		t.Fatalf("TurnStart calls = %v", started)
	}
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
	if len(last.Capabilities) != 4 {
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
		"--include-partial-messages", "--permission-prompt-tool", "stdio", "--permission-mode", "default", "--allow-dangerously-skip-permissions",
		"--thinking", "enabled", "--resume", "s1"}
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

func TestSetConfigAppliesEachKindAndReportsTheValues(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	turn := func(text string) string {
		t.Helper()
		from := len(e.rec.outputs("s1"))
		n := len(e.rec.states("s1"))
		if err := e.m.Prompt("s1", text); err != nil {
			t.Fatal(err)
		}
		e.rec.waitFor(t, "the turn", func() bool {
			s := e.rec.states("s1")
			return len(s) > n+1 && s[len(s)-1] == runtime.StateReady
		})
		return textOf(e, "s1", from)
	}
	args := func(i int) string { return strings.Join(e.starts()[i].Args, " ") }
	current := func(id string) any {
		last, _ := e.rec.lastState("s1")
		for _, o := range last.Config {
			if o.ID == id {
				return o.Current
			}
		}
		t.Fatalf("no option %s in %+v", id, last.Config)
		return nil
	}

	// Before any process, a setting is kept and the next process starts with it.
	if err := e.m.SetConfig("s1", "model", "sonnet"); err != nil {
		t.Fatal(err)
	}
	if current("model") != "sonnet" || current("thinking") != true || current("fast") != false {
		last, _ := e.rec.lastState("s1")
		t.Fatalf("state reports %+v", last.Config)
	}
	if n := len(e.starts()); n != 0 {
		t.Fatalf("setting a value started %d processes", n)
	}
	if got := turn("model"); got != "model: sonnet" {
		t.Fatalf("first turn ran on %q", got)
	}
	// ApplyNextTurn: the model switches in place before the next prompt; nothing restarts.
	if err := e.m.SetConfig("s1", "model", "opus"); err != nil {
		t.Fatal(err)
	}
	if got := turn("model"); got != "model: opus" {
		t.Fatalf("switched turn ran on %q", got)
	}
	if got := turn("model"); got != "model: opus" {
		t.Fatalf("next turn ran on %q", got)
	}
	if n := len(e.starts()); n != 1 {
		t.Fatalf("a model switch restarted the process: %d starts", n)
	}
	// ApplyRestart: the process restarts between turns with every value as it now stands.
	if err := e.m.SetConfig("s1", "thinking", false); err != nil {
		t.Fatal(err)
	}
	if err := e.m.SetConfig("s1", "fast", true); err != nil {
		t.Fatal(err)
	}
	if n := len(e.starts()); n != 1 {
		t.Fatalf("a restart setting restarted at once: %d starts", n)
	}
	if got := turn("model"); got != "model: opus" {
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
	// A setting or value the driver does not declare is refused.
	for _, c := range []struct {
		id    string
		value any
	}{{"model", "gpt-9"}, {"model", true}, {"thinking", "yes"}, {"mode", "dontAsk"}, {"mode", true}, {"permission", "plan"}} {
		if err := e.m.SetConfig("s1", c.id, c.value); !errors.Is(err, runtime.ErrOptionNotDeclared) {
			t.Fatalf("%s=%v: %v", c.id, c.value, err)
		}
	}
}

// gatedDriver holds Start until the gate opens, so a test can change a value while the process is not
// returned yet.
type gatedDriver struct {
	*claude.Driver
	gate chan struct{}
}

func (g gatedDriver) Start(ctx context.Context, spec runtime.StartSpec) (runtime.Process, error) {
	<-g.gate
	return g.Driver.Start(ctx, spec)
}

// A value set while the process starts reaches the first turn: a model switches in place before the prompt, a
// restart setting restarts before it.
func TestValuesSetWhileTheProcessStartsReachTheFirstTurn(t *testing.T) {
	for name, gated := range map[string]bool{"process not returned": true, "waiting for ready": false} {
		t.Run(name, func(t *testing.T) {
			var gate chan struct{}
			if gated {
				gate = make(chan struct{})
			} else {
				t.Setenv("FAKE_CLAUDE_INIT_DELAY_MS", "500")
			}
			e := newEnv(t, func(c *runtime.Config) {
				if gated {
					c.Driver = gatedDriver{Driver: c.Driver.(*claude.Driver), gate: gate}
				}
			})
			e.session("s1")
			if err := e.m.Prompt("s1", "options"); err != nil {
				t.Fatal(err)
			}
			e.rec.waitState(t, "s1", runtime.StateStarting)
			if err := e.m.SetConfig("s1", "model", "opus"); err != nil {
				t.Fatal(err)
			}
			if err := e.m.SetConfig("s1", "fast", true); err != nil {
				t.Fatal(err)
			}
			if gated {
				close(gate)
			}
			e.rec.waitFor(t, "the turn", func() bool {
				s := e.rec.states("s1")
				return len(s) > 2 && s[len(s)-1] == runtime.StateReady
			})
			if got := textOf(e, "s1", 0); got != "model: opus thinking: enabled fast: true" {
				t.Fatalf("the first turn ran on %q", got)
			}
			starts := e.starts()
			if gated {
				// The values were snapshotted before the change, so the first process is replaced.
				if len(starts) != 2 || !strings.Contains(strings.Join(starts[1].Args, " "), "--model opus") {
					t.Fatalf("starts = %+v", starts)
				}
			} else if len(starts) != 2 {
				t.Fatalf("starts = %d, want the restart for fast", len(starts))
			}
			last, _ := e.rec.lastState("s1")
			for _, o := range last.Config {
				if o.ID == "model" && o.Current != "opus" {
					t.Fatalf("reported %v", o.Current)
				}
			}
		})
	}
}

// Switching a running process to `default` goes to the configured model, as starting a process does.
func TestTheDefaultModelIsTheConfiguredOneWhenSwitchingToo(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) {
		c.Driver = claude.New(claude.Options{Bin: runtimetest.Build(t), Model: "haiku"})
	})
	e.session("s1")
	turn := func() string {
		t.Helper()
		from, n := len(e.rec.outputs("s1")), len(e.rec.states("s1"))
		if err := e.m.Prompt("s1", "model"); err != nil {
			t.Fatal(err)
		}
		e.rec.waitFor(t, "the turn", func() bool {
			s := e.rec.states("s1")
			return len(s) > n+1 && s[len(s)-1] == runtime.StateReady
		})
		return textOf(e, "s1", from)
	}
	if got := turn(); got != "model: haiku" {
		t.Fatalf("first turn ran on %q", got)
	}
	for model, want := range map[string]string{"opus": "model: opus", "default": "model: haiku"} {
		if err := e.m.SetConfig("s1", "model", model); err != nil {
			t.Fatal(err)
		}
		if got := turn(); got != want {
			t.Fatalf("after %s the turn ran on %q, want %q", model, got, want)
		}
	}
}

func TestAModelSetWhileTheProcessStartsSwitchesInPlace(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_INIT_DELAY_MS", "500")
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "model"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateStarting)
	if err := e.m.SetConfig("s1", "model", "sonnet"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the turn", func() bool {
		s := e.rec.states("s1")
		return len(s) > 2 && s[len(s)-1] == runtime.StateReady
	})
	if got := textOf(e, "s1", 0); got != "model: sonnet" {
		t.Fatalf("the first turn ran on %q", got)
	}
	if n := len(e.starts()); n != 1 {
		t.Fatalf("starts = %d, want the model switched in place", n)
	}
}

// nowDriver declares one ApplyNow setting whose process refuses every switch.
type nowDriver struct{}

func (nowDriver) Name() string                       { return "now" }
func (nowDriver) Capabilities() []runtime.Capability { return nil }
func (nowDriver) Config() []runtime.ConfigOption {
	return []runtime.ConfigOption{{
		ID: "mode", Name: "Mode", Type: runtime.ConfigSelect, Current: "a", Apply: runtime.ApplyNow,
		Options: []runtime.ConfigValue{{Value: "a", Name: "a"}, {Value: "b", Name: "b"}},
	}}
}
func (nowDriver) Start(context.Context, runtime.StartSpec) (runtime.Process, error) {
	p := &refusingProcess{events: make(chan runtime.ProcessEvent, 4)}
	p.events <- runtime.Ready{}
	return p, nil
}

type refusingProcess struct{ events chan runtime.ProcessEvent }

func (p *refusingProcess) Events() <-chan runtime.ProcessEvent { return p.events }
func (p *refusingProcess) Prompt(runtime.Prompt) error {
	p.events <- runtime.StateChanged{State: runtime.StateRunning}
	p.events <- runtime.TurnResult{}
	p.events <- runtime.StateChanged{State: runtime.StateReady}
	return nil
}
func (p *refusingProcess) Steer(runtime.Prompt) error           { return nil }
func (p *refusingProcess) Set(string, any) error                { return errors.New("refused") }
func (p *refusingProcess) Answer(string, runtime.Verdict) error { return nil }
func (p *refusingProcess) Interrupt() error                     { return nil }
func (p *refusingProcess) Close() error                         { close(p.events); return nil }
func (p *refusingProcess) Kill() error                          { return nil }

func TestAnApplyNowSettingTheProcessRefusesKeepsItsOldValue(t *testing.T) {
	rec := newRecorder()
	m := runtime.NewManager(runtime.Config{
		Driver: nowDriver{},
		Locate: func(string) (runtime.Location, error) { return runtime.Location{Dir: t.TempDir()}, nil },
		Emit:   rec.emit, MaxIdle: 5,
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	if err := m.Prompt("s1", "x"); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "ready", func() bool {
		s := rec.states("s1")
		return len(s) >= 3 && s[len(s)-1] == runtime.StateReady
	})
	if err := m.SetConfig("s1", "mode", "b"); err == nil {
		t.Fatalf("a refused switch was reported as done; states %v", rec.states("s1"))
	}
	last, _ := rec.lastState("s1")
	if last.Config[0].Current != "a" {
		t.Fatalf("reported %v, but the process still has a", last.Config[0].Current)
	}
}

func TestAnIdleSessionWithAChangedValueIsReported(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.SetConfig("s1", "fast", true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range e.m.States() {
		if st.SessionID == "s1" {
			found = true
		}
	}
	if !found {
		t.Fatal("States omits a session whose value was set")
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

// appendForeign writes a line as another process would, once the grace after a turn result has passed.
func appendForeign(t *testing.T, path string) {
	t.Helper()
	time.Sleep(80 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"from a terminal\"}}\n"); err != nil {
		t.Fatal(err)
	}
}

func TestLinesTheProcessWritesAfterATurnAreOwn(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_TRAILING_MS", "100")
	e := newEnv(t, func(c *runtime.Config) { c.OwnGrace = 500 * time.Millisecond })
	file := e.session("s1")
	e.m.Observe("s1", fileSize(t, file))
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	before := fileSize(t, file)
	time.Sleep(200 * time.Millisecond) // the trailing line is written
	if fileSize(t, file) == before {
		t.Fatal("the fake wrote no trailing line")
	}
	e.m.Observe("s1", fileSize(t, file))
	time.Sleep(600 * time.Millisecond) // the grace ends
	e.m.Observe("s1", fileSize(t, file))
	if got := e.m.State("s1"); got != runtime.StateReady {
		t.Fatalf("state = %s", got)
	}
	if err := e.m.Prompt("s1", "say y"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	e.rec.waitFor(t, "second turn", func() bool { return len(e.rec.states("s1")) >= 5 })
	if n := len(e.starts()); n != 1 {
		t.Fatalf("starts = %d", n)
	}
	// A write after the grace is foreign.
	time.Sleep(600 * time.Millisecond)
	appendForeign(t, file)
	e.m.Observe("s1", fileSize(t, file))
	e.rec.waitState(t, "s1", runtime.StateExternal)
}

func TestLinesTheProcessWritesWhenItExitsAreOwn(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_EXIT_LINE", "1")
	e := newEnv(t, func(c *runtime.Config) { c.OwnGrace = 500 * time.Millisecond })
	file := e.session("s1")
	e.m.Observe("s1", fileSize(t, file))
	if err := e.m.Prompt("s1", "say x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	before := fileSize(t, file)
	e.m.Stop("s1")
	e.rec.waitState(t, "s1", runtime.StateIdle)
	if fileSize(t, file) == before {
		t.Fatal("the fake wrote no line on exit")
	}
	e.m.Observe("s1", fileSize(t, file))
	time.Sleep(600 * time.Millisecond)
	e.m.Observe("s1", fileSize(t, file))
	if got := e.m.State("s1"); got != runtime.StateIdle {
		t.Fatalf("state = %s", got)
	}
	if err := e.m.Prompt("s1", "say y"); err != nil {
		t.Fatalf("prompt: %v", err)
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
	if len(got[0].Capabilities) != 4 {
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
	id, err := e.m.Create(e.workdir, "say hello", map[string]any{"model": "opus", "thinking": true, "fast": true})
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
	if len(last.Config) != 4 || last.Config[1].Current != "opus" || last.Config[3].Current != true {
		t.Fatalf("config = %+v", last.Config)
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
	if _, err := e.m.Create(e.workdir, "say x", map[string]any{"model": "gpt-9"}); !errors.Is(err, runtime.ErrOptionNotDeclared) {
		t.Fatalf("model: %v", err)
	}
	if n := len(e.starts()); n != 0 {
		t.Fatalf("a refused session started %d processes", n)
	}
}

func TestNewSessionWhoseProcessExitsFailsWithoutAFile(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_EXIT_ON_START", "1")
	e := newEnv(t, nil)
	id, err := e.m.Create(e.workdir, "say x", nil)
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

// modeOf is the permission mode the session's last reported state carries.
func modeOf(e *env, id string) any {
	last, _ := e.rec.lastState(id)
	for _, o := range last.Config {
		if o.ID == "mode" {
			return o.Current
		}
	}
	return nil
}

func TestThePermissionModeAppliesAtOnceAndEveryProcessStartsWithIt(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.SetConfig("s1", "mode", "plan"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Prompt("s1", "mode"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the first turn", func() bool {
		s := e.rec.states("s1")
		return len(s) > 2 && s[len(s)-1] == runtime.StateReady
	})
	if a := strings.Join(e.starts()[0].Args, " "); !strings.Contains(a, "--permission-mode plan --allow-dangerously-skip-permissions") {
		t.Fatalf("args = %s", a)
	}
	if got := textOf(e, "s1", 0); got != "mode: plan" {
		t.Fatalf("the first turn ran in %q", got)
	}
	// Switching an idle process takes effect before any prompt, so the next process is not needed.
	if err := e.m.SetConfig("s1", "mode", "bypassPermissions"); err != nil {
		t.Fatal(err)
	}
	from := len(e.rec.outputs("s1"))
	n := len(e.rec.states("s1"))
	if err := e.m.Prompt("s1", "mode"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the second turn", func() bool {
		s := e.rec.states("s1")
		return len(s) > n+1 && s[len(s)-1] == runtime.StateReady
	})
	if got := textOf(e, "s1", from); got != "mode: bypassPermissions" || modeOf(e, "s1") != "bypassPermissions" {
		t.Fatalf("the second turn ran in %q, reported %v", got, modeOf(e, "s1"))
	}
	if len(e.starts()) != 1 {
		t.Fatalf("a mode switch started %d processes", len(e.starts()))
	}
}

func TestThePermissionModeSwitchesWhileATurnRuns(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "sleep"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	if err := e.m.SetConfig("s1", "mode", "acceptEdits"); err != nil {
		t.Fatal(err)
	}
	if modeOf(e, "s1") != "acceptEdits" {
		t.Fatalf("reported %v", modeOf(e, "s1"))
	}
	if err := e.m.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the interrupted turn", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady || s[len(s)-1] == runtime.StateFailed
	})
	if err := e.m.Prompt("s1", "mode"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the next turn", func() bool {
		return strings.Contains(textOf(e, "s1", 0), "mode: acceptEdits")
	})
}

// Claude leaving plan mode itself (ExitPlanMode allowed) is a permission request like any other, and the
// session's value follows it.
func TestAVendorInitiatedModeChangeIsReported(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.SetConfig("s1", "mode", "plan"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Prompt("s1", "exitplan 1. do it"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateWaitingPermission)
	e.rec.waitFor(t, "permission request", func() bool { return len(e.rec.permissions("s1")) == 1 })
	reqs := e.rec.permissions("s1")
	if len(reqs) != 1 || reqs[0].Tool != "ExitPlanMode" || !strings.Contains(string(reqs[0].Input), "1. do it") {
		t.Fatalf("permission requests = %+v", reqs)
	}
	if modeOf(e, "s1") != "plan" {
		t.Fatalf("mode before the answer = %v", modeOf(e, "s1"))
	}
	if err := e.m.Permission("s1", reqs[0].RequestID, true); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the mode to change", func() bool { return modeOf(e, "s1") == "default" })
	e.rec.waitState(t, "s1", runtime.StateReady)
	// A denied ExitPlanMode keeps the mode.
	if err := e.m.SetConfig("s1", "mode", "plan"); err != nil {
		t.Fatal(err)
	}
	n := len(e.rec.permissions("s1"))
	if err := e.m.Prompt("s1", "exitplan again"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the second request", func() bool { return len(e.rec.permissions("s1")) > n })
	if err := e.m.Permission("s1", e.rec.permissions("s1")[n].RequestID, false); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	if modeOf(e, "s1") != "plan" {
		t.Fatalf("mode after a denial = %v", modeOf(e, "s1"))
	}
}

// changingDriver's process reports a mode change the driver does not declare, and one it does.
type changingDriver struct{ nowDriver }

func (changingDriver) Start(context.Context, runtime.StartSpec) (runtime.Process, error) {
	events := make(chan runtime.ProcessEvent, 8)
	events <- runtime.Ready{}
	events <- runtime.ConfigChanged{ID: "x", Value: "bogus"}
	events <- runtime.ConfigChanged{ID: "nope", Value: "a"}
	return &refusingProcess{events: events}, nil
}

func TestAVendorChangeToAnUndeclaredValueIsIgnored(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.Driver = changingDriver{} })
	e.session("s1")
	if err := e.m.Prompt("s1", "x"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady) // the turn is over: every event of the process was handled
	last, _ := e.rec.lastState("s1")
	if last.Config[0].Current != "a" {
		t.Fatalf("config = %+v", last.Config)
	}
}

// setDriver starts processes whose Set is the test's function, to hold a switch open.
type setDriver struct {
	nowDriver
	set func(p *setProcess, id string, value any) error
	got chan *setProcess
}

func (d setDriver) Start(context.Context, runtime.StartSpec) (runtime.Process, error) {
	p := &setProcess{events: make(chan runtime.ProcessEvent, 1), set: d.set}
	p.events <- runtime.Ready{}
	d.got <- p
	return p, nil
}

type setProcess struct {
	refusingProcess
	events chan runtime.ProcessEvent
	set    func(p *setProcess, id string, value any) error
}

func (p *setProcess) Events() <-chan runtime.ProcessEvent { return p.events }
func (p *setProcess) Prompt(runtime.Prompt) error {
	go func() {
		p.events <- runtime.StateChanged{State: runtime.StateRunning}
		p.events <- runtime.TurnResult{}
		p.events <- runtime.StateChanged{State: runtime.StateReady}
	}()
	return nil
}
func (p *setProcess) Set(id string, v any) error { return p.set(p, id, v) }
func (p *setProcess) Close() error               { close(p.events); return nil }

func newSetEnv(t *testing.T, set func(p *setProcess, id string, value any) error) (*runtime.Manager, *recorder, *setProcess) {
	t.Helper()
	rec := newRecorder()
	d := setDriver{set: set, got: make(chan *setProcess, 1)}
	m := runtime.NewManager(runtime.Config{
		Driver: d,
		Locate: func(string) (runtime.Location, error) { return runtime.Location{Dir: t.TempDir()}, nil },
		Emit:   rec.emit, MaxIdle: 5,
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	if err := m.Prompt("s1", "x"); err != nil {
		t.Fatal(err)
	}
	p := <-d.got
	rec.waitFor(t, "ready", func() bool {
		s := rec.states("s1")
		return len(s) > 0 && s[len(s)-1] == runtime.StateReady
	})
	return m, rec, p
}

// A switch waits for the process's answer, and the process sends the answer after the events it was streaming:
// the manager must keep taking events meanwhile, or the switch times out and every session stalls behind it.
func TestASwitchDoesNotBlockTheEventsTheProcessSendsBeforeItsAnswer(t *testing.T) {
	m, _, _ := newSetEnv(t, func(p *setProcess, id string, value any) error {
		for i := 0; i < 600; i++ { // far more than the event channel holds
			p.events <- runtime.Output{Kind: runtime.OutputText, Text: "x"}
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- m.SetConfig("s1", "mode", "b") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the switch never returned while the process streamed events")
	}
	other := make(chan struct{})
	go func() { _ = m.SetConfig("s2", "mode", "b"); close(other) }()
	select {
	case <-other:
	case <-time.After(3 * time.Second):
		t.Fatal("another session stalled")
	}
}

// A refused switch rolls back only while the value is still its own: a newer change of the setting stands.
func TestARefusedSwitchDoesNotRollBackANewerChange(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	m, rec, _ := newSetEnv(t, func(p *setProcess, id string, value any) error {
		if calls.Add(1) == 1 {
			<-release
			return errors.New("refused")
		}
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- m.SetConfig("s1", "mode", "b") }()
	for calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if err := m.SetConfig("s1", "mode", "b"); err != nil { // the newer change, taken by the process
		t.Fatal(err)
	}
	close(release)
	<-first
	last, _ := rec.lastState("s1")
	if last.Config[0].Current != "b" {
		t.Fatalf("the newer change was rolled back to %v", last.Config[0].Current)
	}
}

func (r *recorder) commands(id string) []runtime.CommandsEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runtime.CommandsEvent
	for _, ev := range r.events {
		if c, ok := ev.(runtime.CommandsEvent); ok && c.SessionID == id {
			out = append(out, c)
		}
	}
	return out
}

func commandNames(list []runtime.Command) string {
	var names []string
	for _, c := range list {
		names = append(names, c.Kind[:1]+":"+c.Name)
	}
	return strings.Join(names, " ")
}

func TestCommandsAreReportedWhenTheyChange(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_COMMANDS", "1")
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say hi"); err != nil {
		t.Fatal(err)
	}
	// initialize lists them without the skill marks; the init message of the turn marks the skills.
	e.rec.waitFor(t, "commands with skills", func() bool {
		cs := e.rec.commands("s1")
		return len(cs) > 0 && strings.Contains(commandNames(cs[len(cs)-1].Commands), "s:fix-bug")
	})
	cs := e.rec.commands("s1")
	last := cs[len(cs)-1]
	if last.Dir != e.workdir {
		t.Fatalf("dir = %q, want %q", last.Dir, e.workdir)
	}
	if got, want := commandNames(last.Commands), "c:compact c:context c:deploy s:fix-bug s:pdf:extract"; got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	n := len(e.rec.commands("s1"))

	// The same list again is not reported twice; a changed one is.
	e.rec.waitFor(t, "ready", func() bool { s, _ := e.rec.lastState("s1"); return s.State == runtime.StateReady })
	if err := e.m.Prompt("s1", "newcmd done"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "lint", func() bool {
		cs := e.rec.commands("s1")
		return len(cs) > n && strings.Contains(commandNames(cs[len(cs)-1].Commands), "c:lint")
	})
	if got := e.m.Commands(); len(got) != 1 || got[0].SessionID != "s1" || !strings.Contains(commandNames(got[0].Commands), "c:lint") {
		t.Fatalf("Commands() = %+v", got)
	}
}

func TestNoCommandsAreReportedWhenTheVendorListsNone(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say hi"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	if cs := e.rec.commands("s1"); len(cs) != 0 {
		t.Fatalf("commands = %+v", cs)
	}
}

func outputText(e *env, id string) string {
	var b strings.Builder
	for _, o := range e.rec.outputs(id) {
		if o.Kind == runtime.OutputText {
			b.WriteString(o.Text)
		}
	}
	return b.String()
}

// openPlan puts a session in plan mode and has Claude ask to leave it.
func openPlan(t *testing.T, e *env, id string) runtime.PermissionRequest {
	t.Helper()
	e.session(id)
	if err := e.m.SetConfig(id, "mode", "plan"); err != nil {
		t.Fatal(err)
	}
	n := len(e.rec.permissions(id))
	if err := e.m.Prompt(id, "exitplan 1. do it"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the plan request", func() bool { return len(e.rec.permissions(id)) > n })
	return e.rec.permissions(id)[n]
}

// The driver declares ExitPlanMode as a plan exit with the plan and the modes approval may switch to, and
// other tools declare nothing (ADR 0019).
func TestTheDriverDeclaresAPlanExit(t *testing.T) {
	e := newEnv(t, nil)
	req := openPlan(t, e, "s1")
	pe := req.PlanExit
	if pe == nil || pe.Plan != "1. do it" || pe.Option != "mode" || pe.Default != "acceptEdits" || !pe.FreshContext ||
		!reflect.DeepEqual(pe.Modes, []string{"acceptEdits", "default", "auto"}) {
		t.Fatalf("plan exit = %+v", pe)
	}
	if err := e.m.Decide("s1", req.RequestID, runtime.Verdict{Allow: true, Mode: "acceptEdits"}); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
}

func TestApprovingAPlanSwitchesToTheChosenMode(t *testing.T) {
	e := newEnv(t, nil)
	req := openPlan(t, e, "s1")
	if err := e.m.Decide("s1", req.RequestID, runtime.Verdict{Allow: true, Mode: "bypassPermissions"}); !errors.Is(err, runtime.ErrInvalidMode) {
		t.Fatalf("a mode the request does not offer: %v", err)
	}
	if err := e.m.Decide("s1", req.RequestID, runtime.Verdict{Mode: "auto"}); !errors.Is(err, runtime.ErrInvalidMode) {
		t.Fatalf("a mode on a refusal: %v", err)
	}
	if len(e.m.Pending()) != 1 {
		t.Fatalf("a rejected answer closed the request: %+v", e.m.Pending())
	}
	if err := e.m.Decide("s1", req.RequestID, runtime.Verdict{Allow: true, Mode: "acceptEdits"}); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "the mode to change", func() bool { return modeOf(e, "s1") == "acceptEdits" })
	e.rec.waitState(t, "s1", runtime.StateReady)
}

func TestKeepingPlanningSendsTheFeedbackAndStaysInPlan(t *testing.T) {
	e := newEnv(t, nil)
	req := openPlan(t, e, "s1")
	if err := e.m.Decide("s1", req.RequestID, runtime.Verdict{Message: "split step 2"}); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	if got := outputText(e, "s1"); !strings.Contains(got, "REFUSED: split step 2") {
		t.Fatalf("the model was told %q", got)
	}
	if modeOf(e, "s1") != "plan" {
		t.Fatalf("mode = %v", modeOf(e, "s1"))
	}
}

// A plan waits for the user as long as it takes: the automatic denial of other requests does not apply.
func TestAPlanRequestIsNeverDeniedByTheTimeout(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.PermissionTimeout = 200 * time.Millisecond })
	req := openPlan(t, e, "s1")
	time.Sleep(600 * time.Millisecond)
	if len(e.m.Pending()) != 1 {
		t.Fatalf("pending = %+v", e.m.Pending())
	}
	if err := e.m.Decide("s1", req.RequestID, runtime.Verdict{Allow: true}); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
}

package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

var (
	_ runtime.Driver     = (*Driver)(nil)
	_ runtime.IDAssigner = (*Driver)(nil)
	_ runtime.Versioned  = (*Driver)(nil)
)

func TestMain(m *testing.M) {
	if os.Getenv("HOPLET_FAKE_CODEX") == "1" {
		runFake()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// harness is a driver whose executable is the fake app server and the log of what the fake received.
type harness struct {
	t   *testing.T
	d   *Driver
	log string
	img string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, log: filepath.Join(t.TempDir(), "log"), img: filepath.Join(t.TempDir(), "images")}
	t.Setenv("HOPLET_FAKE_CODEX", "1")
	t.Setenv("FAKE_CODEX_LOG", h.log)
	h.d = New(Options{Bin: exe, ImageDir: h.img})
	return h
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// startNew opens a new thread and waits until the process is ready.
func (h *harness) startNew(cfg map[string]any) (string, runtime.Process) {
	h.t.Helper()
	id, p, err := h.d.StartNew(ctx(h.t), runtime.StartSpec{Dir: h.t.TempDir(), New: true, Config: cfg})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = p.Kill() })
	expect[runtime.Ready](h.t, p)
	return id, p
}

// received are the params of the requests and notifications of one method the fake received, in order.
func (h *harness) received(method string) []map[string]any {
	h.t.Helper()
	data, _ := os.ReadFile(h.log)
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil || m["method"] != method {
			continue
		}
		params, _ := m["params"].(map[string]any)
		out = append(out, params)
	}
	return out
}

// answers are the responses the client sent to the fake's server requests.
func (h *harness) answers() []map[string]any {
	data, _ := os.ReadFile(h.log)
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["method"] == nil && m["result"] != nil {
			out = append(out, m)
		}
	}
	return out
}

func at(m map[string]any, path ...string) any {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

// nextEvent returns the first event for which match is true, skipping the ones before.
func nextEvent(t *testing.T, p runtime.Process, match func(runtime.ProcessEvent) bool) runtime.ProcessEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatal("events closed before the expected event")
			}
			if match(ev) {
				return ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for an event")
		}
	}
}

func expect[T runtime.ProcessEvent](t *testing.T, p runtime.Process) T {
	t.Helper()
	var out T
	nextEvent(t, p, func(ev runtime.ProcessEvent) bool {
		v, ok := ev.(T)
		if ok {
			out = v
		}
		return ok
	})
	return out
}

func running(ev runtime.ProcessEvent) bool {
	s, ok := ev.(runtime.StateChanged)
	return ok && s.State == runtime.StateRunning
}

func ready(ev runtime.ProcessEvent) bool {
	s, ok := ev.(runtime.StateChanged)
	return ok && s.State == runtime.StateReady
}

func TestVersionIsReported(t *testing.T) {
	h := newHarness(t)
	v, tested, err := h.d.Version(ctx(t))
	if err != nil || v != "0.163.0" || !tested {
		t.Fatalf("Version = %q, %v, %v", v, tested, err)
	}
}

func TestUnsupportedVersionIsReportedAndNotRun(t *testing.T) {
	h := newHarness(t)
	t.Setenv("FAKE_CODEX_VERSION", "0.1.0")
	if _, _, err := h.d.Version(ctx(t)); err == nil || !strings.Contains(err.Error(), "unsupported codex-cli version") {
		t.Fatalf("Version error = %v", err)
	}
	if _, _, err := h.d.StartNew(ctx(t), runtime.StartSpec{Dir: t.TempDir(), New: true}); err == nil {
		t.Fatal("StartNew ran an unsupported version")
	}
	if _, err := h.d.Start(ctx(t), runtime.StartSpec{SessionID: "abc", Dir: t.TempDir()}); err == nil {
		t.Fatal("Start ran an unsupported version")
	}
	if err := h.d.LoadModels(ctx(t)); err == nil {
		t.Fatal("LoadModels ran an unsupported version")
	}
	if _, err := os.Stat(h.log); !os.IsNotExist(err) {
		t.Fatal("an app server was started")
	}
}

func TestVersionOutput(t *testing.T) {
	if got := parseVersionOutput("codex-cli 0.163.0\n"); got != "0.163.0" {
		t.Fatalf("parse = %q", got)
	}
	if parseVersionOutput(" \n") != "" {
		t.Fatal("empty output has no version")
	}
}

func TestConfigDeclaresCodexSettings(t *testing.T) {
	d := New(Options{})
	cfg := d.Config()
	var ids []string
	for _, o := range cfg {
		ids = append(ids, o.ID)
		if o.Apply != runtime.ApplyNextTurn {
			t.Errorf("%s applies %s", o.ID, o.Apply)
		}
	}
	if !reflect.DeepEqual(ids, []string{"mode", "model", "effort", "collaboration"}) {
		t.Fatalf("options = %v", ids)
	}
	var values []string
	for _, v := range cfg[0].Options {
		values = append(values, v.Value)
		if (v.Value == "full-access") != (v.Risk == "high") {
			t.Errorf("risk of %s = %q", v.Value, v.Risk)
		}
	}
	if !reflect.DeepEqual(values, []string{"read-only", "workspace-write", "auto-review", "full-access"}) || cfg[0].Current != "workspace-write" {
		t.Fatalf("mode = %v, current %v", values, cfg[0].Current)
	}
	if len(cfg[1].Options) != 1 || cfg[1].Options[0].Value != "default" {
		t.Fatalf("models before they are read: %v", cfg[1].Options)
	}
	// Without the hook socket the rules act on prompts only.
	if got := d.Capabilities(); len(got) != 5 || got[4] != runtime.CapRulesPromptsOnly {
		t.Fatalf("capabilities = %v", got)
	}
}

func TestLoadModelsOffersTheModelsOnce(t *testing.T) {
	h := newHarness(t)
	if err := h.d.LoadModels(ctx(t)); err != nil {
		t.Fatal(err)
	}
	cfg := h.d.Config()
	var models, efforts []string
	for _, v := range cfg[1].Options {
		models = append(models, v.Value)
	}
	for _, v := range cfg[2].Options {
		efforts = append(efforts, v.Value)
	}
	if !reflect.DeepEqual(models, []string{"default", "gpt-test", "gpt-small"}) {
		t.Fatalf("models = %v", models)
	}
	if !reflect.DeepEqual(efforts, []string{"default", "low", "medium", "high"}) {
		t.Fatalf("efforts = %v", efforts)
	}
	if err := h.d.LoadModels(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if n := len(h.received("model/list")); n != 1 {
		t.Fatalf("model/list sent %d times", n)
	}
}

func TestStartNewAssignsTheThreadID(t *testing.T) {
	h := newHarness(t)
	id, _ := h.startNew(nil)
	if id != "thr-new" {
		t.Fatalf("id = %q", id)
	}
	init := h.received("initialize")
	if len(init) != 1 || at(init[0], "capabilities", "experimentalApi") != true {
		t.Fatalf("initialize = %v", init)
	}
	if len(h.received("initialized")) != 1 {
		t.Fatal("initialized was not sent")
	}
	ts := h.received("thread/start")
	if len(ts) != 1 || ts[0]["approvalPolicy"] != "on-request" || ts[0]["sandbox"] != "workspace-write" || ts[0]["approvalsReviewer"] != "user" {
		t.Fatalf("thread/start = %v", ts)
	}
	if _, ok := ts[0]["model"]; ok {
		t.Fatal("thread/start overrides the model of a default session")
	}
	if len(h.d.Config()[1].Options) != 3 {
		t.Fatal("the process did not offer the models")
	}
}

func TestStartRefusesANewSessionID(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.Start(ctx(t), runtime.StartSpec{SessionID: "x", Dir: t.TempDir(), New: true}); err == nil {
		t.Fatal("Start created a session")
	}
}

func TestResumeThread(t *testing.T) {
	h := newHarness(t)
	p, err := h.d.Start(ctx(t), runtime.StartSpec{
		SessionID: "abc", Dir: t.TempDir(),
		Config: map[string]any{"mode": "full-access", "model": "gpt-small"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	expect[runtime.Ready](t, p)
	r := h.received("thread/resume")
	if len(r) != 1 || r[0]["threadId"] != "abc" || r[0]["excludeTurns"] != true {
		t.Fatalf("thread/resume = %v", r)
	}
	if r[0]["approvalPolicy"] != "never" || r[0]["sandbox"] != "danger-full-access" || r[0]["model"] != "gpt-small" {
		t.Fatalf("thread/resume settings = %v", r[0])
	}
	if len(h.received("thread/start")) != 0 {
		t.Fatal("a resume started a thread")
	}
}

func TestResumeOfAnUnknownThreadFails(t *testing.T) {
	h := newHarness(t)
	_, err := h.d.Start(ctx(t), runtime.StartSpec{SessionID: "missing", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no rollout found") {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptRunsATurn(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	if err := p.Prompt(runtime.Prompt{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, p, running)
	out := expect[runtime.Output](t, p)
	if out.Kind != runtime.OutputText || out.Text != "hi" {
		t.Fatalf("output = %+v", out)
	}
	if res := expect[runtime.TurnResult](t, p); res.Failed {
		t.Fatalf("result = %+v", res)
	}
	nextEvent(t, p, ready)

	ts := h.received("turn/start")
	if len(ts) != 1 {
		t.Fatalf("turn/start = %v", ts)
	}
	g := ts[0]
	if g["threadId"] != "thr-new" || at(g, "collaborationMode", "mode") != "default" || at(g, "collaborationMode", "settings", "model") != "gpt-test" {
		t.Fatalf("turn/start = %v", g)
	}
	if g["approvalPolicy"] != "on-request" || at(g, "sandboxPolicy", "type") != "workspaceWrite" || g["approvalsReviewer"] != "user" {
		t.Fatalf("turn/start mode = %v", g)
	}
	if _, ok := g["model"]; ok {
		t.Fatal("a default model is overridden")
	}
	if _, ok := g["effort"]; ok {
		t.Fatal("a default effort is overridden")
	}
	in := g["input"].([]any)[0].(map[string]any)
	if in["type"] != "text" || in["text"] != "hello" {
		t.Fatalf("input = %v", in)
	}
}

func TestPromptSendsImagesAsLocalFiles(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	png := []byte("\x89PNG-bytes")
	if err := p.Prompt(runtime.Prompt{Text: "hello", Images: []runtime.Image{{MediaType: "image/png", Data: png}}}); err != nil {
		t.Fatal(err)
	}
	expect[runtime.TurnResult](t, p)
	in := h.received("turn/start")[0]["input"].([]any)
	if len(in) != 2 {
		t.Fatalf("input = %v", in)
	}
	img := in[1].(map[string]any)
	path, _ := img["path"].(string)
	if img["type"] != "localImage" || filepath.Dir(path) != h.img || !strings.HasSuffix(path, ".png") {
		t.Fatalf("image input = %v", img)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(png) {
		t.Fatalf("image file = %q, %v", data, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("image mode = %v", st.Mode().Perm())
	}
	_ = p.Close()
	expect[runtime.Exited](t, p)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the image outlived the process")
	}
	if err := p.Prompt(runtime.Prompt{Text: "x", Images: []runtime.Image{{MediaType: "image/bmp"}}}); err == nil {
		t.Fatal("an unsupported image type was accepted")
	}
}

func TestRefusedPromptFailsTheTurn(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	if err := p.Prompt(runtime.Prompt{Text: "bad"}); err != nil {
		t.Fatal(err)
	}
	res := expect[runtime.TurnResult](t, p)
	if !res.Failed || !strings.Contains(res.Error, "turn refused") {
		t.Fatalf("result = %+v", res)
	}
	nextEvent(t, p, ready)
}

func TestFailedTurnCarriesTheError(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "fail"})
	if res := expect[runtime.TurnResult](t, p); !res.Failed || res.Error != "boom" {
		t.Fatalf("result = %+v", res)
	}
}

func TestSettingsApplyAtTheNextTurn(t *testing.T) {
	cases := []struct {
		mode, approval, policy, reviewer string
	}{
		{"read-only", "on-request", "readOnly", "user"},
		{"workspace-write", "on-request", "workspaceWrite", "user"},
		{"auto-review", "on-request", "workspaceWrite", "auto_review"},
		{"full-access", "never", "dangerFullAccess", "user"},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			h := newHarness(t)
			_, p := h.startNew(nil)
			for id, v := range map[string]string{"mode": c.mode, "model": "gpt-small", "effort": "low", "collaboration": "plan"} {
				if err := p.Set(id, v); err != nil {
					t.Fatal(err)
				}
			}
			_ = p.Prompt(runtime.Prompt{Text: "echo"})
			expect[runtime.TurnResult](t, p)
			g := h.received("turn/start")[0]
			if g["approvalPolicy"] != c.approval || at(g, "sandboxPolicy", "type") != c.policy || g["approvalsReviewer"] != c.reviewer {
				t.Fatalf("mode %s sent %v", c.mode, g)
			}
			if g["model"] != "gpt-small" || g["effort"] != "low" || at(g, "collaborationMode", "mode") != "plan" ||
				at(g, "collaborationMode", "settings", "model") != "gpt-small" || at(g, "collaborationMode", "settings", "reasoning_effort") != "low" {
				t.Fatalf("turn/start = %v", g)
			}
		})
	}
}

func TestEffortTheModelLacksIsLeftOut(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Set("model", "gpt-small")
	_ = p.Set("effort", "high")
	_ = p.Prompt(runtime.Prompt{Text: "echo"})
	expect[runtime.TurnResult](t, p)
	g := h.received("turn/start")[0]
	if _, ok := g["effort"]; ok || at(g, "collaborationMode", "settings", "reasoning_effort") != nil {
		t.Fatalf("turn/start = %v", g)
	}
}

func TestSetRefusesWhatTheDriverDoesNotDeclare(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	for _, c := range []struct {
		id string
		v  any
	}{{"mode", "yolo"}, {"collaboration", "review"}, {"model", ""}, {"mode", 3}, {"bogus", "x"}} {
		if err := p.Set(c.id, c.v); err == nil {
			t.Errorf("Set(%q, %v) succeeded", c.id, c.v)
		}
	}
}

func TestSettingsCodexChangesAreReported(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "settings"})
	got := map[string]any{}
	for {
		ev := nextEvent(t, p, func(ev runtime.ProcessEvent) bool {
			_, c := ev.(runtime.ConfigChanged)
			_, r := ev.(runtime.TurnResult)
			return c || r
		})
		if _, done := ev.(runtime.TurnResult); done {
			break
		}
		c := ev.(runtime.ConfigChanged)
		got[c.ID] = c.Value
	}
	want := map[string]any{"mode": "full-access", "collaboration": "plan", "effort": "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
}

func TestSteerReachesTheRunningTurn(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "slow"})
	nextEvent(t, p, running)
	if err := p.Steer(runtime.Prompt{Text: "more"}); err != nil {
		t.Fatal(err)
	}
	out := expect[runtime.Output](t, p)
	if out.Text != "steered: more" {
		t.Fatalf("output = %+v", out)
	}
	expect[runtime.TurnResult](t, p)
	st := h.received("turn/steer")
	if len(st) != 1 || st[0]["expectedTurnId"] != "turn-1" || st[0]["threadId"] != "thr-new" {
		t.Fatalf("turn/steer = %v", st)
	}
	if len(h.received("turn/start")) != 1 {
		t.Fatal("a steer started a turn")
	}
}

func TestSteerWithNoTurnStartsOne(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	if err := p.Steer(runtime.Prompt{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, p, running)
	if out := expect[runtime.Output](t, p); out.Text != "hi" {
		t.Fatalf("output = %+v", out)
	}
	expect[runtime.TurnResult](t, p)
	if len(h.received("turn/steer")) != 0 || len(h.received("turn/start")) != 1 {
		t.Fatal("the message was not run as a turn")
	}
}

func TestSteerFallsBackWhenCodexFindsNoTurn(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	// The turn ended on Codex's side before the process saw it: forge the turn it still believes in.
	proc := p.(*process)
	proc.mu.Lock()
	proc.turnID = "turn-gone"
	proc.mu.Unlock()
	if err := p.Steer(runtime.Prompt{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if out := expect[runtime.Output](t, p); out.Text != "hi" {
		t.Fatalf("output = %+v", out)
	}
	if len(h.received("turn/steer")) != 1 || len(h.received("turn/start")) != 1 {
		t.Fatal("the failed steer did not fall back to a turn")
	}
}

func TestInterruptEndsTheTurn(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "slow"})
	nextEvent(t, p, running)
	if err := p.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if res := expect[runtime.TurnResult](t, p); res.Failed {
		t.Fatalf("an interrupted turn is not a failure: %+v", res)
	}
	nextEvent(t, p, ready)
	it := h.received("turn/interrupt")
	if len(it) != 1 || it[0]["turnId"] != "turn-1" || it[0]["threadId"] != "thr-new" {
		t.Fatalf("turn/interrupt = %v", it)
	}
}

func TestCommandApprovalAccepted(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "cmd"})
	call := expect[runtime.Output](t, p)
	if call.Kind != runtime.OutputToolCall || call.Tool != "commandExecution" || call.ToolUseID != "item-1" {
		t.Fatalf("call = %+v", call)
	}
	req := expect[runtime.PermissionRequest](t, p)
	if req.RequestID != "0" || req.Tool != "commandExecution" || req.ToolUseID != "item-1" || req.PlanExit != nil {
		t.Fatalf("request = %+v", req)
	}
	var in map[string]any
	_ = json.Unmarshal(req.Input, &in)
	if in["command"] != "ls" || in["cwd"] != "/work" || in["reason"] != "list" {
		t.Fatalf("input = %v", in)
	}
	if err := p.Answer("0", runtime.Verdict{Allow: true}); err != nil {
		t.Fatal(err)
	}
	if err := p.Answer("0", runtime.Verdict{Allow: true}); err == nil {
		t.Fatal("a request was answered twice")
	}
	nextEvent(t, p, running) // the turn goes on once Codex resolved the request
	res := expect[runtime.Output](t, p)
	if res.Kind != runtime.OutputToolResult || res.ToolUseID != "item-1" || res.Text != "a.txt\n" || res.IsError {
		t.Fatalf("result = %+v", res)
	}
	expect[runtime.TurnResult](t, p)
	a := h.answers()
	if len(a) != 1 || a[0]["id"] != float64(0) || at(a[0], "result", "decision") != "accept" {
		t.Fatalf("answers = %v", a)
	}
}

func TestFileChangeApprovalDeclinedSteersTheMessage(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "edit"})
	req := expect[runtime.PermissionRequest](t, p)
	if req.RequestID != "1" || req.Tool != "fileChange" {
		t.Fatalf("request = %+v", req)
	}
	var in map[string]any
	_ = json.Unmarshal(req.Input, &in)
	if in["grantRoot"] != "/work" || in["reason"] != "edit" {
		t.Fatalf("input = %v", in)
	}
	if err := p.Answer("1", runtime.Verdict{Allow: false, Message: "use another file"}); err != nil {
		t.Fatal(err)
	}
	if out := nextEvent(t, p, func(ev runtime.ProcessEvent) bool {
		o, ok := ev.(runtime.Output)
		return ok && o.Kind == runtime.OutputText
	}).(runtime.Output); out.Text != "steered: use another file" {
		t.Fatalf("output = %+v", out)
	}
	expect[runtime.TurnResult](t, p)
	a := h.answers()
	if len(a) != 1 || at(a[0], "result", "decision") != "decline" {
		t.Fatalf("answers = %v", a)
	}
	st := h.received("turn/steer")
	if len(st) != 1 || st[0]["expectedTurnId"] != "turn-1" {
		t.Fatalf("turn/steer = %v", st)
	}
}

func TestInterruptCancelsAWaitingApproval(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "waitcmd"})
	req := expect[runtime.PermissionRequest](t, p)
	if err := p.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if c := expect[runtime.PermissionCancelled](t, p); c.RequestID != req.RequestID {
		t.Fatalf("cancelled = %+v", c)
	}
	if res := expect[runtime.TurnResult](t, p); res.Failed {
		t.Fatalf("result = %+v", res)
	}
	if err := p.Answer(req.RequestID, runtime.Verdict{Allow: true}); err == nil {
		t.Fatal("a resolved request was answered")
	}
}

// planExit runs a plan turn and returns its plan-exit request.
func planExit(t *testing.T, h *harness, p runtime.Process) runtime.PermissionRequest {
	t.Helper()
	if err := p.Set("collaboration", "plan"); err != nil {
		t.Fatal(err)
	}
	_ = p.Prompt(runtime.Prompt{Text: "plan"})
	if res := expect[runtime.TurnResult](t, p); res.Failed {
		t.Fatalf("result = %+v", res)
	}
	return expect[runtime.PermissionRequest](t, p)
}

func TestPlanExitApproved(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	req := planExit(t, h, p)
	pe := req.PlanExit
	if req.RequestID != "plan:turn-1" || req.Tool != "plan" || pe == nil {
		t.Fatalf("request = %+v", req)
	}
	want := runtime.PlanExit{Plan: "1. do it", Option: "mode", Modes: []string{"read-only", "workspace-write", "auto-review"}, Default: "workspace-write"}
	if !reflect.DeepEqual(*pe, want) {
		t.Fatalf("plan exit = %+v", *pe)
	}
	if err := p.Answer(req.RequestID, runtime.Verdict{Allow: true, Mode: "full-access"}); err == nil {
		t.Fatal("a plan was approved into full access")
	}
	if err := p.Answer("plan:turn-1", runtime.Verdict{Allow: true, Mode: "auto-review"}); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for len(got) < 2 {
		c := expect[runtime.ConfigChanged](t, p)
		got[c.ID] = c.Value
	}
	if !reflect.DeepEqual(got, map[string]any{"collaboration": "default", "mode": "auto-review"}) {
		t.Fatalf("changes = %v", got)
	}
	ts := h.received("turn/start")
	if len(ts) != 2 {
		t.Fatalf("turn/start = %v", ts)
	}
	g := ts[1]
	if at(g, "collaborationMode", "mode") != "default" || g["approvalsReviewer"] != "auto_review" || at(g, "sandboxPolicy", "type") != "workspaceWrite" ||
		!strings.HasPrefix(inputText(g), "Implement") {
		t.Fatalf("second turn = %v", g)
	}
	if err := p.Answer("plan:turn-1", runtime.Verdict{Allow: true}); err == nil {
		t.Fatal("a plan was answered twice")
	}
}

func TestPlanExitRefusedStaysInPlanMode(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	req := planExit(t, h, p)
	if err := p.Answer(req.RequestID, runtime.Verdict{Allow: false, Message: "make it smaller"}); err != nil {
		t.Fatal(err)
	}
	expect[runtime.TurnResult](t, p)
	ts := h.received("turn/start")
	if len(ts) != 2 || inputText(ts[1]) != "make it smaller" || at(ts[1], "collaborationMode", "mode") != "plan" {
		t.Fatalf("turn/start = %v", ts)
	}
}

func TestInterruptCancelsAPlanExit(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	req := planExit(t, h, p)
	if err := p.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if c := expect[runtime.PermissionCancelled](t, p); c.RequestID != req.RequestID {
		t.Fatalf("cancelled = %+v", c)
	}
	nextEvent(t, p, ready)
	if len(h.received("turn/interrupt")) != 0 {
		t.Fatal("an interrupt was sent with no turn running")
	}
}

func TestPlanTurnWithoutAPlanEndsReady(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Set("collaboration", "plan")
	_ = p.Prompt(runtime.Prompt{Text: "echo"})
	expect[runtime.TurnResult](t, p)
	nextEvent(t, p, ready)
}

func TestExitReportsTheError(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "exit"})
	ex := expect[runtime.Exited](t, p)
	if ex.Err == nil || !strings.Contains(ex.Err.Error(), "gave up") {
		t.Fatalf("exit = %v", ex.Err)
	}
	if _, ok := <-p.Events(); ok {
		t.Fatal("events stayed open after Exited")
	}
	if err := p.Prompt(runtime.Prompt{Text: "hello"}); err == nil {
		t.Fatal("an exited process took a prompt")
	}
}

func TestCloseEndsTheProcessCleanly(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	pid := p.(interface{ PID() int }).PID()
	if pid <= 0 {
		t.Fatalf("pid = %d", pid)
	}
	_ = p.Close()
	if ex := expect[runtime.Exited](t, p); ex.Err != nil {
		t.Fatalf("exit = %v", ex.Err)
	}
}

func TestKillEndsTheProcess(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	_ = p.Prompt(runtime.Prompt{Text: "slow"})
	nextEvent(t, p, running)
	_ = p.Kill()
	for range p.Events() {
	}
}

func TestUnsupportedServerRequestIsRefused(t *testing.T) {
	h := newHarness(t)
	_, p := h.startNew(nil)
	proc := p.(*process)
	proc.handleRequest(json.RawMessage("7"), "item/tool/call", nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(readFile(h.log), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil && m["id"] == float64(7) && at(m, "error", "code") == float64(-32601) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the request was not refused")
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

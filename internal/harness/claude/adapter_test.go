package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// fakeClaude writes a script that answers --version like claude and counts
// its runs in dir/runs.
func fakeClaude(t *testing.T, version string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho run >> " + filepath.Join(dir, "runs") + "\necho '" + version + " (Claude Code)'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func runs(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "runs"))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "run\n")
}

func TestProbeCachesPerBinary(t *testing.T) {
	bin, dir := fakeClaude(t, "2.1.312")
	h := &Harness{Binary: bin}
	ctx := context.Background()
	for range 3 {
		info, err := h.Probe(ctx)
		if err != nil || info.Version != "2.1.312" || !info.Supported || !info.Capabilities.PlanMode || info.Capabilities.Fast {
			t.Fatalf("probe %+v %v", info, err)
		}
	}
	if n := runs(t, dir); n != 1 {
		t.Fatalf("claude ran %d times for 3 probes", n)
	}
	// A new binary in its place is probed again.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '9.0.0 (Claude Code)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(bin, later, later); err != nil {
		t.Fatal(err)
	}
	if info, err := h.Probe(ctx); err != nil || info.Version != "9.0.0" || info.Supported {
		t.Fatalf("probe after replacing the binary %+v %v", info, err)
	}
	if _, err := (&Harness{Binary: filepath.Join(dir, "missing")}).Probe(ctx); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing binary: %v", err)
	}
	if _, err := parseVersion([]byte("hello")); err == nil {
		t.Fatal("parsed a non-claude version")
	}
}

func TestLaunch(t *testing.T) {
	h := &Harness{Binary: "/h/claude"}
	for name, tc := range map[string]struct {
		spec harness.Spec
		want []string
		bad  bool
	}{
		"confirm":     {spec: harness.Spec{Mode: proto.ModeConfirm}, want: []string{"--permission-mode", "default"}},
		"auto review": {spec: harness.Spec{Mode: proto.ModeAutoReview}, want: []string{"--permission-mode", "auto"}},
		"full auto":   {spec: harness.Spec{Mode: proto.ModeFullAuto}, want: []string{"--permission-mode", "bypassPermissions"}},
		"plan":        {spec: harness.Spec{Mode: proto.ModeAutoEdit, Plan: true}, want: []string{"--permission-mode", "plan"}},
		"effort":      {spec: harness.Spec{Mode: proto.ModeConfirm, Effort: proto.EffortXHigh}, want: []string{"--effort", "xhigh"}},
		"resume":      {spec: harness.Spec{Mode: proto.ModeConfirm, Resume: "abc", Model: "sonnet"}, want: []string{"--model", "sonnet", "--resume", "abc"}},
		"ultra":       {spec: harness.Spec{Mode: proto.ModeConfirm, Effort: proto.EffortUltra}, bad: true},
		"fast":        {spec: harness.Spec{Mode: proto.ModeConfirm, Fast: true}, bad: true},
		"bogus mode":  {spec: harness.Spec{Mode: "read_only"}, bad: true},
	} {
		tc.spec.WorkDir = "/work"
		tc.spec.Env = []string{"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=desktop", "ANTHROPIC_BASE_URL=https://gw", "DISABLE_AUTOUPDATER=0"}
		l, err := h.Launch(tc.spec)
		if tc.bad {
			if err == nil {
				t.Errorf("%s: launched %v", name, l.Command)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cmd := strings.Join(l.Command, " ")
		for _, must := range []string{"/h/claude -p", "--input-format stream-json", "--output-format stream-json", "--permission-prompt-tool stdio", strings.Join(tc.want, " ")} {
			if !strings.Contains(cmd, must) {
				t.Errorf("%s: %q lacks %q", name, cmd, must)
			}
		}
		if l.Dir != "/work" || !slices.Equal(l.Env, []string{"PATH=/bin", "ANTHROPIC_BASE_URL=https://gw", "DISABLE_AUTOUPDATER=1"}) {
			t.Errorf("%s: dir %q env %v", name, l.Dir, l.Env)
		}
	}
}

// liveRun is a run over a fake stdio, for driving it line by line.
func liveRun(t *testing.T, spec harness.Spec) (*replayer, *fakeStdio) {
	t.Helper()
	io := newFakeStdio()
	p := &replayer{t: t, io: io, run: newRun(io, spec)}
	t.Cleanup(func() { p.run.Close() })
	return p, io
}

func lastWrite(t *testing.T, io *fakeStdio) map[string]any {
	t.Helper()
	w := io.written()
	if len(w) == 0 {
		t.Fatal("nothing written")
	}
	var m map[string]any
	if err := json.Unmarshal(w[len(w)-1], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// events returns the recorded events without the writes the run made.
func events(p *replayer) []string {
	var out []string
	for _, g := range p.golden {
		if !strings.HasPrefix(g, "write ") {
			out = append(out, g)
		}
	}
	return out
}

func TestPlanAndModeTracking(t *testing.T) {
	ctx := context.Background()
	p, io := liveRun(t, harness.Spec{Mode: proto.ModeAutoReview, Plan: true})
	// Changing the mode in plan mode only changes what is restored.
	if err := p.run.SetMode(ctx, proto.ModeFullAuto); err != nil || len(io.written()) != 0 {
		t.Fatalf("SetMode in plan mode wrote %d lines (%v)", len(io.written()), err)
	}
	// claude leaves plan mode by itself after a plan is approved, back to
	// its default: the session's mode is restored and the change reported.
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"default"}`))
	if want := `proto.ModeChanged {"permission":"full_auto","plan":false}`; len(events(p)) != 1 || events(p)[0] != want {
		t.Fatalf("events %q, want %q", events(p), want)
	}
	if req := lastWrite(t, io)["request"].(map[string]any); req["mode"] != "bypassPermissions" {
		t.Fatalf("restore wrote %v", req)
	}
	// claude confirming the restored mode is not a change.
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"bypassPermissions"}`))
	// An approval answered with a session-wide mode change.
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"acceptEdits"}`))
	// claude entering plan mode by itself.
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"plan"}`))
	want := []string{
		`proto.ModeChanged {"permission":"full_auto","plan":false}`,
		`proto.ModeChanged {"permission":"auto_edit","plan":false}`,
		`proto.ModeChanged {"permission":"auto_edit","plan":true}`,
	}
	if !slices.Equal(events(p), want) {
		t.Fatalf("events %q", events(p))
	}
	// Hoplet turning plan mode off restores auto_edit, and claude's
	// confirmation is not reported back.
	if err := p.run.SetPlan(ctx, false); err != nil {
		t.Fatal(err)
	}
	if req := lastWrite(t, io)["request"].(map[string]any); req["mode"] != "acceptEdits" {
		t.Fatalf("SetPlan(false) wrote %v", req)
	}
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"acceptEdits"}`))
	if len(events(p)) != 3 {
		t.Fatalf("Hoplet's own change was reported: %q", events(p))
	}
}

func TestCommandsAndRefusals(t *testing.T) {
	ctx := context.Background()
	p, io := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	if err := p.run.SetEffort(ctx, proto.EffortMax); err != nil {
		t.Fatal(err)
	}
	if req := lastWrite(t, io)["request"].(map[string]any); req["subtype"] != "apply_flag_settings" || req["settings"].(map[string]any)["effortLevel"] != "max" {
		t.Fatalf("SetEffort wrote %v", req)
	}
	for name, err := range map[string]error{
		"ultra":  p.run.SetEffort(ctx, proto.EffortUltra),
		"fast":   p.run.SetFast(ctx, true),
		"steer":  p.run.Steer(ctx, "x"),
		"answer": p.run.Answer(ctx, "q", "a"),
	} {
		if !errors.Is(err, harness.ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := p.run.Respond(ctx, "a1", "maybe"); err == nil {
		t.Fatal("unknown option accepted")
	}
	// An approval the run never saw (a daemon that reattached after it was
	// asked) is answered without its input.
	if err := p.run.Respond(ctx, "unseen", optionAllow); err != nil {
		t.Fatal(err)
	}
	if resp := lastWrite(t, io)["response"].(map[string]any); resp["request_id"] != "unseen" || len(resp["response"].(map[string]any)) != 1 {
		t.Fatalf("answer to an unseen approval %v", resp)
	}
	// A control request Hoplet does not handle is refused, not left waiting.
	p.push(harness.Stdout, []byte(`{"type":"control_request","request_id":"r9","request":{"subtype":"hook_callback"}}`))
	if resp := lastWrite(t, io)["response"].(map[string]any); resp["subtype"] != "error" || resp["request_id"] != "r9" {
		t.Fatalf("refusal %v", resp)
	}
	if len(events(p)) != 1 || !strings.HasPrefix(events(p)[0], "proto.Error") {
		t.Fatalf("events %q", events(p))
	}
}

func TestApprovalOptionsAndCancel(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	p.push(harness.Stdout, []byte(`{"type":"control_request","request_id":"s1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/w/a"},"permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"t1"}}`))
	p.push(harness.Stdout, []byte(`{"type":"control_request","request_id":"s2","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"permission_suggestions":[{"type":"addRules","destination":"localSettings"}]}}`))
	p.push(harness.Stdout, []byte(`{"type":"control_request","request_id":"s3","request":{"subtype":"can_use_tool","tool_name":"WebFetch","input":{"url":"https://x"}}}`))
	p.push(harness.Stdout, []byte(`{"type":"control_cancel_request","request_id":"s3"}`))
	p.push(harness.Stdout, []byte(`{"type":"control_cancel_request","request_id":"unknown"}`))
	want := []string{
		`proto.ApprovalRequested {"id":"s1","kind":"file_change","title":"Write /w/a","tool_call_id":"t1","options":[{"id":"allow","label":"Allow once","scope":"once"},{"id":"always","label":"Allow for this session","scope":"session"},{"id":"deny","label":"Deny","scope":"deny"}]}`,
		`proto.ApprovalRequested {"id":"s2","kind":"command","title":"Bash ls","options":[{"id":"allow","label":"Allow once","scope":"once"},{"id":"always","label":"Always allow","scope":"always"},{"id":"deny","label":"Deny","scope":"deny"}]}`,
		`proto.ApprovalRequested {"id":"s3","kind":"tool","title":"WebFetch https://x","options":[{"id":"allow","label":"Allow once","scope":"once"},{"id":"deny","label":"Deny","scope":"deny"}]}`,
		`proto.ApprovalResolved {"id":"s3","outcome":"expired"}`,
	}
	if !slices.Equal(p.golden, want) {
		t.Fatalf("events:\n%s", strings.Join(p.golden, "\n"))
	}
}

func TestExitMidTurnAborts(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"init","session_id":"n1","permissionMode":"default"}`))
	p.offset += 2
	p.io.lines <- harness.Line{Kind: harness.Exit, Code: 1, Offset: p.offset}
	for ev := range p.run.Events() {
		p.record(ev)
	}
	want := []string{"native n1 proto.TurnStarted {}", `proto.TurnEnded {"reason":"aborted","message":"claude exited with code 1"}`}
	if !slices.Equal(p.golden, want) {
		t.Fatalf("events %q", p.golden)
	}
	if err := p.run.Err(); err != nil {
		t.Fatalf("err after an exit: %v", err)
	}
}

func TestResultWithUnexpectedErrorsShape(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	p.push(harness.Stdout, []byte(`{"type":"result","is_error":true,"errors":[{"code":1,"text":"boom"}]}`))
	if len(p.golden) != 1 || !strings.HasPrefix(p.golden[0], `proto.TurnEnded {"reason":"failed"`) || !strings.Contains(p.golden[0], "boom") {
		t.Fatalf("events %q", p.golden)
	}
}

func TestStdoutClosingWithoutExitDetaches(t *testing.T) {
	p, io := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	close(io.lines)
	for range p.run.Events() {
	}
	if err := p.run.Err(); !errors.Is(err, harness.ErrDetached) {
		t.Fatalf("err %v", err)
	}
}

func TestMalformedLineDoesNotStopTranslation(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	p.push(harness.Stdout, []byte(`{not json`))
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"init","session_id":"n1","permissionMode":"default"}`))
	want := []string{`proto.Error {"message":"unparseable claude output"}`, "native n1 proto.TurnStarted {}"}
	if !slices.Equal(p.golden, want) {
		t.Fatalf("events %q", p.golden)
	}
}

func TestPlanExitRestoresNonDefaultMode(t *testing.T) {
	p, io := liveRun(t, harness.Spec{Mode: proto.ModeAutoEdit})
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"plan"}`))
	p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"default"}`))
	want := []string{
		`proto.ModeChanged {"permission":"auto_edit","plan":true}`,
		`proto.ModeChanged {"permission":"auto_edit","plan":false}`,
	}
	if got := events(p); !slices.Equal(got, want) {
		t.Fatalf("events %q", got)
	}
	if req := lastWrite(t, io)["request"].(map[string]any); req["subtype"] != "set_permission_mode" || req["mode"] != "acceptEdits" {
		t.Fatalf("restore wrote %v", req)
	}
}

func TestOptionAlwaysNeedsSuggestions(t *testing.T) {
	p, io := liveRun(t, harness.Spec{Mode: proto.ModeConfirm})
	if err := p.run.Respond(context.Background(), "unseen", optionAlways); err == nil {
		t.Fatal("always accepted for an unknown approval")
	}
	if n := len(io.written()); n != 0 {
		t.Fatalf("wrote %d lines", n)
	}
}

func TestUnmappableModeReportedOnce(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{Mode: proto.ModeAutoEdit})
	for range 3 {
		p.push(harness.Stdout, []byte(`{"type":"system","subtype":"status","permissionMode":"dontAsk"}`))
	}
	if len(p.golden) != 1 || !strings.HasPrefix(p.golden[0], "proto.ModeChanged") {
		t.Fatalf("events %q", p.golden)
	}
}

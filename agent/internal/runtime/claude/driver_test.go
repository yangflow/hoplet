package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func TestArgsAreTheDocumentedFlags(t *testing.T) {
	got := strings.Join(New(Options{}).Args(runtime.StartSpec{SessionID: "abc"}), " ")
	want := "-p --input-format stream-json --output-format stream-json --verbose --include-partial-messages " +
		"--permission-prompt-tool stdio --permission-mode default --allow-dangerously-skip-permissions --thinking enabled --resume abc"
	if got != want {
		t.Fatalf("args = %q", got)
	}
}

func TestModelOptionAddsTheModelFlag(t *testing.T) {
	got := strings.Join(New(Options{Model: "haiku"}).Args(runtime.StartSpec{SessionID: "abc"}), " ")
	if !strings.Contains(got, "--model haiku --thinking enabled --resume abc") {
		t.Fatalf("args = %q", got)
	}
}

func TestNewSessionArgsCreateTheSessionAndApplyTheConfig(t *testing.T) {
	spec := runtime.StartSpec{
		SessionID: "abc", New: true,
		Config: map[string]any{"mode": "plan", "model": "opus", "thinking": true, "fast": true},
	}
	got := strings.Join(New(Options{Model: "haiku"}).Args(spec), " ")
	want := "-p --input-format stream-json --output-format stream-json --verbose --include-partial-messages " +
		"--permission-prompt-tool stdio --permission-mode plan --allow-dangerously-skip-permissions --model opus --thinking enabled " +
		`--settings {"fastMode":true} --session-id abc`
	if got != want {
		t.Fatalf("args = %q", got)
	}
	if strings.Contains(got, "--resume") {
		t.Fatalf("a new session resumes nothing: %q", got)
	}
	spec.Config = map[string]any{"model": "default", "thinking": false, "fast": false}
	if got := strings.Join(New(Options{}).Args(spec), " "); !strings.Contains(got, "--thinking disabled --session-id abc") ||
		strings.Contains(got, "--model") || strings.Contains(got, "--settings") {
		t.Fatalf("args = %q", got)
	}
}

func TestConfigDeclaresModeModelThinkingAndFastInOrder(t *testing.T) {
	cfg := New(Options{}).Config()
	if len(cfg) != 4 {
		t.Fatalf("config = %+v", cfg)
	}
	mode, model, thinking, fast := cfg[0], cfg[1], cfg[2], cfg[3]
	if mode.ID != "mode" || mode.Type != runtime.ConfigSelect || mode.Category != "mode" ||
		mode.Apply != runtime.ApplyNow || mode.Current != "default" {
		t.Fatalf("mode = %+v", mode)
	}
	var modes []string
	for _, v := range mode.Options {
		modes = append(modes, v.Value)
		if (v.Value == "bypassPermissions") != (v.Risk == "high") || v.Name == "" || v.Description == "" {
			t.Fatalf("mode value = %+v", v)
		}
	}
	if strings.Join(modes, ",") != "default,acceptEdits,plan,auto,bypassPermissions" {
		t.Fatalf("mode values = %v", modes)
	}
	if model.ID != "model" || model.Type != runtime.ConfigSelect || model.Category != "model" ||
		model.Apply != runtime.ApplyNextTurn || model.Current != "default" {
		t.Fatalf("model = %+v", model)
	}
	var values []string
	for _, v := range model.Options {
		values = append(values, v.Value)
	}
	if strings.Join(values, ",") != "default,fable,opus,sonnet" {
		t.Fatalf("model values = %v", values)
	}
	if thinking.ID != "thinking" || thinking.Type != runtime.ConfigBoolean || thinking.Category != "thought_level" ||
		thinking.Apply != runtime.ApplyRestart || thinking.Current != true {
		t.Fatalf("thinking = %+v", thinking)
	}
	if fast.ID != "fast" || fast.Type != runtime.ConfigBoolean || fast.Category != "model_config" ||
		fast.Apply != runtime.ApplyRestart || fast.Current != false {
		t.Fatalf("fast = %+v", fast)
	}
	cfg[1].Options[0].Value = "x" // a copy
	if New(Options{}).Config()[1].Options[0].Value != "default" {
		t.Fatal("Config returned its own slice")
	}
}

// A process that answers set_permission_mode like Claude Code and reports a change as a status event.
const modeScript = `
read init
echo '{"type":"control_response","response":{"subtype":"success","request_id":"hoplet-init","response":{}}}'
echo '{"type":"system","subtype":"init","permissionMode":"manual"}'
while read line; do
  id=$(echo "$line" | sed 's/.*"request_id":"\([^"]*\)".*/\1/')
  case "$line" in
    *'"mode":"plan"'*)
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"'$id'","response":{"mode":"plan"}}}'
      echo '{"type":"system","subtype":"status","status":null,"permissionMode":"plan"}' ;;
    *'"mode":"bypassPermissions"'*)
      echo '{"type":"control_response","response":{"subtype":"error","request_id":"'$id'","error":"not launched","error_code":"bypass_not_launched"}}' ;;
    *'"mode":"dontAsk"'*)
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"'$id'","response":{"mode":"dontAsk"}}}'
      echo '{"type":"system","subtype":"status","status":null,"permissionMode":"dontAsk"}' ;;
  esac
done
`

func TestSetModeWaitsForTheAnswerAndReturnsARefusal(t *testing.T) {
	p := start(t, script(t, modeScript))
	if err := p.Set("mode", "bypassPermissions"); err == nil || !strings.Contains(err.Error(), "not launched") {
		t.Fatalf("a refused switch: %v", err)
	}
	if err := p.Set("mode", "plan"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("fast", true); err == nil {
		t.Fatal("a setting the process cannot switch was accepted")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	var changes []runtime.ConfigChanged
	for _, ev := range collect(t, p) {
		if c, ok := ev.(runtime.ConfigChanged); ok {
			changes = append(changes, c)
		}
	}
	// The init message reports the mode (`manual` is `default`), and a status event the change to plan.
	if len(changes) != 2 || changes[0] != (runtime.ConfigChanged{ID: "mode", Value: "default"}) ||
		changes[1] != (runtime.ConfigChanged{ID: "mode", Value: "plan"}) {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestSetModeEndsWhenTheProcessExits(t *testing.T) {
	p := start(t, script(t, `
read init
echo '{"type":"control_response","response":{"subtype":"success","request_id":"hoplet-init","response":{}}}'
read request
`))
	done := make(chan error, 1)
	go func() { done <- p.Set("mode", "plan") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a switch the process never answered succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Set did not return when the process exited")
	}
}

func TestCapabilities(t *testing.T) {
	caps := New(Options{}).Capabilities()
	want := []runtime.Capability{runtime.CapPermissions, runtime.CapInterrupt, runtime.CapImages}
	if len(caps) != len(want) {
		t.Fatalf("caps = %v", caps)
	}
	for i := range want {
		if caps[i] != want[i] {
			t.Fatalf("caps = %v", caps)
		}
	}
}

func TestVersionRule(t *testing.T) {
	for _, tc := range []struct {
		version string
		tested  bool
		refused bool
	}{
		{"2.1.289", true, false},
		{"2.1.295", true, false},
		{"2.1.296", false, false},
		{"2.2.0", false, false},
		{"3.0.1", false, false},
		{"2.1.288", false, true},
		{"2.0.999", false, true},
		{"junk", false, true},
	} {
		tested, err := CheckVersion(tc.version)
		if (err != nil) != tc.refused || tested != tc.tested {
			t.Fatalf("%s: tested %v err %v", tc.version, tested, err)
		}
		if err != nil && !strings.Contains(err.Error(), MinVersion) {
			t.Fatalf("%s: reason %v does not name the minimum", tc.version, err)
		}
	}
}

func TestResultTextBoundsContent(t *testing.T) {
	long := `"` + strings.Repeat("é", runtime.MaxOutputText) + `"`
	text, truncated := resultText([]byte(long))
	if !truncated || len(text) > runtime.MaxOutputText || !strings.HasSuffix(text, "é") {
		t.Fatalf("len %d truncated %v", len(text), truncated)
	}
	text, truncated = resultText([]byte(`[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]`))
	if text != "ab" || truncated {
		t.Fatalf("text %q", text)
	}
}

// script writes a shell script that stands in for claude.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	src := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo '2.1.289 (Claude Code)'; exit 0; fi\n" + body
	if err := os.WriteFile(path, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, p runtime.Process) []runtime.ProcessEvent {
	t.Helper()
	var out []runtime.ProcessEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatalf("timed out; events so far: %+v", out)
		}
	}
}

func start(t *testing.T, bin string) runtime.Process {
	t.Helper()
	p, err := New(Options{Bin: bin}).Start(context.Background(), runtime.StartSpec{SessionID: "s", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnknownControlRequestsGetAnErrorResponse(t *testing.T) {
	reply := filepath.Join(t.TempDir(), "reply")
	bin := script(t, `
read init
echo '{"type":"control_response","response":{"subtype":"success","request_id":"hoplet-init","response":{}}}'
echo '{"type":"control_request","request_id":"h1","request":{"subtype":"hook_callback"}}'
read answer
echo "$answer" > `+reply+`
`)
	evs := collect(t, start(t, bin))
	if _, ok := evs[0].(runtime.Ready); !ok {
		t.Fatalf("first event = %#v", evs[0])
	}
	got, _ := os.ReadFile(reply)
	if !strings.Contains(string(got), `"request_id":"h1"`) || !strings.Contains(string(got), `"subtype":"error"`) {
		t.Fatalf("reply = %s", got)
	}
}

func TestRefusedInitializeEndsTheProcess(t *testing.T) {
	bin := script(t, `
read init
echo '{"type":"control_response","response":{"subtype":"error","request_id":"hoplet-init","error":"nope"}}'
exec sleep 30
`)
	evs := collect(t, start(t, bin))
	exit := evs[len(evs)-1].(runtime.Exited)
	if exit.Err == nil || !strings.Contains(exit.Err.Error(), "nope") {
		t.Fatalf("exit = %v", exit.Err)
	}
}

func TestUnparsableAndUnknownLinesAreIgnored(t *testing.T) {
	bin := script(t, `
read init
echo 'not json'
echo '{"type":"future_event","x":1}'
echo '{"type":"system","subtype":"session_state_changed","state":"unknown_state"}'
echo '{"type":"control_response","response":{"subtype":"success","request_id":"hoplet-init","response":{}}}'
`)
	evs := collect(t, start(t, bin))
	if len(evs) != 2 {
		t.Fatalf("events = %+v", evs)
	}
}

func TestMissingBinaryIsAnError(t *testing.T) {
	_, err := New(Options{Bin: filepath.Join(t.TempDir(), "absent")}).Start(context.Background(), runtime.StartSpec{SessionID: "s", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error")
	}
}

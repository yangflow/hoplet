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
		"--permission-prompt-tool stdio --resume abc"
	if got != want {
		t.Fatalf("args = %q", got)
	}
}

func TestModelOptionAddsTheModelFlag(t *testing.T) {
	got := strings.Join(New(Options{Model: "haiku"}).Args(runtime.StartSpec{SessionID: "abc"}), " ")
	if !strings.Contains(got, "--model haiku --resume abc") {
		t.Fatalf("args = %q", got)
	}
}

func TestNewSessionArgsCreateTheSessionAndApplyTheOptions(t *testing.T) {
	on, off := true, false
	spec := runtime.StartSpec{
		SessionID: "abc", New: true,
		Options: runtime.StartOptions{Model: "opus", Thinking: &on, Fast: true},
	}
	got := strings.Join(New(Options{Model: "haiku"}).Args(spec), " ")
	want := "-p --input-format stream-json --output-format stream-json --verbose --include-partial-messages " +
		"--permission-prompt-tool stdio --model opus --thinking enabled " +
		`--settings {"fastMode":true} --session-id abc`
	if got != want {
		t.Fatalf("args = %q", got)
	}
	if strings.Contains(got, "--resume") {
		t.Fatalf("a new session resumes nothing: %q", got)
	}
	spec.Options = runtime.StartOptions{Thinking: &off}
	if got := strings.Join(New(Options{}).Args(spec), " "); !strings.Contains(got, "--thinking disabled --session-id abc") ||
		strings.Contains(got, "--model") || strings.Contains(got, "--settings") {
		t.Fatalf("args = %q", got)
	}
}

func TestModelsAreTheDocumentedAliases(t *testing.T) {
	got := New(Options{}).Models()
	if strings.Join(got, ",") != "fable,opus,sonnet" {
		t.Fatalf("models = %v", got)
	}
	got[0] = "x" // a copy
	if New(Options{}).Models()[0] != "fable" {
		t.Fatal("Models returned its own slice")
	}
}

func TestCapabilities(t *testing.T) {
	caps := New(Options{}).Capabilities()
	want := []runtime.Capability{
		runtime.CapPermissions, runtime.CapInterrupt, runtime.CapThinking, runtime.CapFast, runtime.CapModels,
		runtime.CapImages, runtime.CapSetModel,
	}
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

package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// initAnswerLine is claude's answer to initialize, cut to three models
// (one without effort) and the fields the adapter reads.
const initAnswerLine = `{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{"commands":[],"models":[` +
	`{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)","description":"Opus 5.5 · Best for everyday, complex tasks","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"],"supportsFastMode":true},` +
	`{"value":"haiku","resolvedModel":"claude-haiku-5-5","displayName":"Haiku 5.5","description":"Fastest","supportsEffort":true,"supportedEffortLevels":["low","bogus"]},` +
	`{"value":"old","resolvedModel":"claude-old","displayName":"Old","description":"No effort"}]}}}`

func TestAttachAsksForModels(t *testing.T) {
	io := newFakeStdio()
	h := &Harness{}
	r, err := h.Attach(context.Background(), harness.Spec{Session: "s", Mode: proto.ModeConfirm}, io)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	writes := io.written()
	if len(writes) != 1 {
		t.Fatalf("attach wrote %q, want one initialize", writes)
	}
	var req requestLine
	if err := json.Unmarshal(writes[0], &req); err != nil || req.Type != "control_request" || req.RequestID == "" {
		t.Fatalf("initialize %s: %v", writes[0], err)
	}
	line := []byte(strings.Replace(initAnswerLine, "%s", req.RequestID, 1))
	go func() { io.lines <- harness.Line{Kind: harness.Stdout, Data: line, Offset: 1} }()
	<-r.Events() // the line's event follows its handling

	want := []proto.ModelInfo{
		{ID: "default", Model: "claude-opus-5-5", Name: "Default (recommended)", Description: "Opus 5.5 · Best for everyday, complex tasks", Default: true, Fast: true,
			Efforts: []proto.Effort{proto.EffortLow, proto.EffortMedium, proto.EffortHigh, proto.EffortXHigh, proto.EffortMax}},
		{ID: "haiku", Model: "claude-haiku-5-5", Name: "Haiku 5.5", Description: "Fastest", Efforts: []proto.Effort{proto.EffortLow}},
		{ID: "old", Model: "claude-old", Name: "Old", Description: "No effort", NoEffort: true},
	}
	h.mu.Lock()
	got := h.models
	h.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models %+v, want %+v", got, want)
	}
}

func TestToolResultRidesOnItsEvent(t *testing.T) {
	io := newFakeStdio()
	r := newRun(io, harness.Spec{Session: "s", Mode: proto.ModeConfirm}, nil)
	defer r.Close()
	lines := []string{
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"a.go\nb.go"}]}}`,
	}
	go func() {
		for i, l := range lines {
			io.lines <- harness.Line{Kind: harness.Stdout, Data: []byte(l), Offset: int64(i + 1)}
		}
	}()
	for {
		ev := <-r.Events()
		tc, ok := ev.Payload.(proto.ToolCall)
		if !ok || tc.Status == proto.ToolRunning {
			if len(ev.Output) > 0 {
				t.Fatalf("output on %+v", ev)
			}
			continue
		}
		if string(ev.Output) != "a.go\nb.go" {
			t.Fatalf("output %q on %+v", ev.Output, tc)
		}
		return
	}
}

// Models asks a claude of its own before any session, once per binary: it
// reads the initialize answer, sends no message, and drops claude's
// session variables from the environment.
func TestModelsWithoutASession(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	answer := strings.Replace(initAnswerLine, "%s", "models", 1)
	script := "#!/bin/sh\necho run >> " + filepath.Join(dir, "runs") + "\nenv > " + filepath.Join(dir, "env") +
		"\nread line\necho \"$line\" > " + filepath.Join(dir, "request") + "\ncat <<'EOF'\n" + answer + "\nEOF\nsleep 5\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	h := &Harness{Binary: bin}
	h.UseEnv(func() []string { return []string{"PATH=/usr/bin:/bin", "CLAUDECODE=1"} })
	ctx := context.Background()
	for range 2 {
		models, err := h.Models(ctx)
		if err != nil || len(models) != 3 || !models[0].Fast || models[1].Name != "Haiku 5.5" {
			t.Fatalf("models %+v %v", models, err)
		}
	}
	if n := runs(t, dir); n != 1 {
		t.Fatalf("claude ran %d times for one binary", n)
	}
	req, _ := os.ReadFile(filepath.Join(dir, "request"))
	if !strings.Contains(string(req), `"subtype":"initialize"`) || strings.Contains(string(req), `"type":"user"`) {
		t.Fatalf("asked %s", req)
	}
	if env, _ := os.ReadFile(filepath.Join(dir, "env")); strings.Contains(string(env), "CLAUDECODE") {
		t.Fatal("claude's session variables reached the listing claude")
	}
}

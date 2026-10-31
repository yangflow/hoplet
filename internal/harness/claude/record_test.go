package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// scriptedClaude is a stand-in for claude's stream-json mode: a user
// message mentioning "tool" asks for approval, anything else gets a result;
// an answer gets a result.
const scriptedClaude = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"user"'*tool*)
      echo '{"type":"system","subtype":"init","session_id":"s1","permissionMode":"default","skills":["mine"]}'
      echo '{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"permission_suggestions":[{"type":"addRules","destination":"localSettings"}]}}'
      ;;
    *'"type":"user"'*)
      echo '{"type":"system","subtype":"init","session_id":"s1","permissionMode":"default"}'
      echo '{"type":"result","subtype":"success","result":"ok"}'
      ;;
    *'"type":"control_response"'*)
      echo "got $line" >&2
      echo '{"type":"result","subtype":"success","result":"done"}'
      ;;
  esac
done
`

func TestRecord(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(scriptedClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Record(context.Background(), RecordConfig{
		Binary: bin, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}, Spec: harness.Spec{Mode: proto.ModeConfirm},
		Steps: []Step{
			{Do: "user", Text: "hello"}, {Do: "wait", For: "result"},
			{Do: "user", Text: "use a tool"}, {Do: "wait", For: "can_use_tool", Answer: "always"}, {Do: "wait", For: "result"},
		},
	}, &out)
	if err != nil {
		t.Fatalf("record: %v\n%s", err, out.String())
	}
	var dirs []string
	var answer string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e Entry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, e.Dir)
		if e.Dir == "in" && strings.Contains(e.Line, "control_response") {
			answer = e.Line
		}
	}
	// stderr is read on its own, so only stdout and stdin keep their order.
	var ordered []string
	for _, d := range dirs {
		if d != "err" {
			ordered = append(ordered, d)
		}
	}
	if got := strings.Join(ordered, " "); got != "in out out in out out in out exit" || strings.Count(strings.Join(dirs, " "), "err") != 1 {
		t.Fatalf("directions %q", dirs)
	}
	if !strings.Contains(answer, `"updatedPermissions":[{"destination":"localSettings","type":"addRules"}]`) && !strings.Contains(answer, `"updatedPermissions":[{"type":"addRules","destination":"localSettings"}]`) {
		t.Fatalf("always answer %s", answer)
	}

	if err := Record(context.Background(), RecordConfig{
		Binary: bin, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}, Spec: harness.Spec{Mode: proto.ModeConfirm},
		Steps: []Step{{Do: "wait", For: "result", Timeout: 1}},
	}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no result within") {
		t.Fatalf("a wait that never ends: %v", err)
	}
}

func TestSanitize(t *testing.T) {
	entries := []Entry{
		{Dir: "in", Line: `{"type":"user","message":{"role":"user","content":"edit /home/me/proj/a.go"}}`},
		{Dir: "out", Line: `{"type":"control_response","response":{"subtype":"success","request_id":"h1","response":{"commands":[1],"account":{"email":"x"}}}}`},
		{Dir: "out", Line: `{"type":"system","subtype":"init","session_id":"s","cwd":"/home/me/proj","skills":["mine"],"memory_paths":{"auto":"/home/me/.claude"}}`},
		{Dir: "out", Line: `{"type":"rate_limit_event"}`},
		{Dir: "out", Line: `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"/home/m"}}}`},
		{Dir: "out", Line: `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"abc"}]}}`},
		{Dir: "err", Line: "debug /home/me"},
		{Dir: "exit", Line: "0"},
	}
	var in bytes.Buffer
	for _, e := range entries {
		b, _ := json.Marshal(e)
		in.Write(append(b, '\n'))
	}
	var out bytes.Buffer
	if err := Sanitize(&in, &out, "/home/me/proj", "/home/me"); err != nil {
		t.Fatal(err)
	}
	// Check what the lines say, not their escaped form.
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e Entry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, e.Dir+" "+e.Line)
	}
	got := strings.Join(lines, "\n") + "\n"
	for _, bad := range []string{"/home/me", "account", "skills", "memory_paths", "rate_limit", "input_json_delta", "signature", "debug"} {
		if strings.Contains(got, bad) {
			t.Errorf("sanitized recording still has %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{"edit /work/a.go", `"cwd":"/work"`, `"response":{}`, "exit 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitized recording lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 5 {
		t.Errorf("%d lines kept, want 5:\n%s", n, got)
	}
}

// sanitized runs Sanitize over entries and returns "dir line" per kept line.
func sanitized(t *testing.T, dir, home string, entries ...Entry) []string {
	t.Helper()
	var in bytes.Buffer
	for _, e := range entries {
		b, _ := json.Marshal(e)
		in.Write(append(b, '\n'))
	}
	var out bytes.Buffer
	if err := Sanitize(&in, &out, dir, home); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, e.Dir+" "+e.Line)
	}
	return lines
}

func TestReplacePath(t *testing.T) {
	for _, tt := range []struct{ s, from, to, want string }{
		{"/Users/ann/x and /Users/anna/x", "/Users/ann", "/Users/me", "/Users/me/x and /Users/anna/x"},
		{`"/Users/ann"`, "/Users/ann", "/Users/me", `"/Users/me"`},
		{"/Users/ann", "/Users/ann", "/Users/me", "/Users/me"},
		{"/Users/ann-old", "/Users/ann", "/Users/me", "/Users/ann-old"},
		{"-Users-ann-proj", "-Users-ann", "-Users-me", "-Users-me-proj"},
		{"-Users-anna", "-Users-ann", "-Users-me", "-Users-anna"},
	} {
		if got := replacePath(tt.s, tt.from, tt.to); got != tt.want {
			t.Errorf("replacePath(%q, %q) = %q, want %q", tt.s, tt.from, got, tt.want)
		}
	}
}

func TestSanitizeScrubsPathForms(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.ReplaceAll(resolved, "/", "-")
	home := "/home/me"
	got := strings.Join(sanitized(t, dir, home,
		Entry{Dir: "out", Line: `{"type":"assistant","a":"` + dir + `/x","b":"` + resolved + `/y","c":"~/.claude/projects/` + slug + `","d":"/home/mex","e":"-home-me-z"}`},
	), "\n")
	for _, bad := range []string{dir, resolved, slug, "/home/me/", "-home-me-"} {
		if strings.Contains(got, bad+"\"") || strings.Contains(got, bad+"/") || got == bad {
			t.Errorf("still has %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{`"a":"/work/x"`, `"b":"/work/y"`, "projects/-work", `"d":"/home/mex"`, `"e":"-Users-me-z"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestSanitizeDropsHookLines(t *testing.T) {
	got := sanitized(t, "", "",
		Entry{Dir: "out", Line: `{"type":"system","subtype":"hook_started","output":"secret"}`},
		Entry{Dir: "out", Line: `{"type":"system","subtype":"hook_response","output":"secret"}`},
		Entry{Dir: "out", Line: `{"type":"system","subtype":"status","status":"x"}`},
	)
	if len(got) != 1 || strings.Contains(strings.Join(got, ""), "secret") || !strings.Contains(got[0], "status") {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeCutsAtAnUnansweredApproval(t *testing.T) {
	req := func(id string) Entry {
		return Entry{Dir: "out", Line: `{"type":"control_request","request_id":"` + id + `","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{}}}`}
	}
	reply := Entry{Dir: "in", Line: `{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{}}}`}
	result := Entry{Dir: "out", Line: `{"type":"result","subtype":"success"}`}
	exit := Entry{Dir: "exit", Line: "0"}

	if got := sanitized(t, "", "", req("r1"), reply, result, exit); len(got) != 4 {
		t.Fatalf("answered approval cut the recording: %q", got)
	}
	got := sanitized(t, "", "", result, req("r2"), result, exit)
	if len(got) != 1 {
		t.Fatalf("unanswered approval kept %q", got)
	}
}

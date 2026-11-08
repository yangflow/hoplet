package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

// fakeTools answers like a daemon serving one tool.
type fakeTools struct{ done chan struct{} }

func (f fakeTools) Done() <-chan struct{} { return f.done }

func (fakeTools) Call(_ context.Context, method string, params, result any) error {
	var v any
	switch method {
	case proto.MethodToolsList:
		v = proto.ToolsListResult{Tools: []proto.ToolInfo{{
			Name: "sessions_list", Description: "List sessions.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}}}
	case proto.MethodToolsCall:
		if p := params.(proto.ToolsCallParams); p.Name != "sessions_list" {
			v = proto.ToolsCallResult{Text: "no tool named " + p.Name, IsError: true}
			break
		}
		v = proto.ToolsCallResult{Text: "[]"}
	default:
		return &proto.APIError{Code: proto.CodeUnauthorized, Message: method}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

// canonical re-encodes a JSON line with sorted keys.
func canonical(t *testing.T, line []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(line, &v); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSpace(b), []byte("\n"))
}

// TestMCPClaudeHandshake replays what Claude Code 2.1.316 sent to the
// server (a server/discover probe, no initialize) and expects the answers
// it got then, in any order.
func TestMCPClaudeHandshake(t *testing.T) {
	in := readLines(t, filepath.Join("testdata", "mcp", "claude-2.1.316.in.jsonl"))
	want := readLines(t, filepath.Join("testdata", "mcp", "claude-2.1.316.out.jsonl"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- serveMCP(context.Background(), fakeTools{}, &mcp.IOTransport{Reader: inR, Writer: outW})
		outW.Close()
	}()
	go func() {
		for _, l := range in {
			if _, err := inW.Write(append(l, '\n')); err != nil {
				return
			}
		}
	}()
	var got, wantC []string
	sc := bufio.NewScanner(outR)
	sc.Buffer(nil, 1<<20)
	for len(got) < len(want) && sc.Scan() {
		got = append(got, canonical(t, sc.Bytes()))
	}
	for _, l := range want {
		wantC = append(wantC, canonical(t, l))
	}
	inW.Close()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	slices.Sort(got)
	slices.Sort(wantC)
	if !slices.Equal(got, wantC) {
		t.Fatalf("answers differ\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantC, "\n"))
	}
}

func TestMCPEndsWhenTheDaemonGoes(t *testing.T) {
	inR, inW := io.Pipe()
	defer inW.Close()
	done := make(chan struct{})
	served := make(chan error, 1)
	go func() {
		served <- serveMCP(context.Background(), fakeTools{done: done}, &mcp.IOTransport{Reader: inR, Writer: nopWriteCloser{io.Discard}})
	}()
	close(done)
	select {
	case err := <-served:
		if !errors.Is(err, errDaemonGone) {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("still serving after the daemon went")
	}
}

// mcpCommand is `hoplet mcp` (this test binary) for home with token.
func mcpCommand(home, token string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "mcp", "--home", home)
	cmd.Env = append(os.Environ(), "HOPLET_TEST_MAIN=1", tools.TokenEnv+"="+token)
	return cmd
}

func TestMCPBridge(t *testing.T) {
	h, _ := testHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(ctx, proto.Session{ID: "s-mcp-bridge", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "fake"}, Directory: h, Owner: proto.OwnerMe, Origin: proto.OriginMe})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionToken(ctx, sess.ID, store.TokenHash("good-token"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	startDaemonProcess(t, h)

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.CommandTransport{Command: mcpCommand(h, "good-token")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		// The simulator tools appear only on a macOS host.
		if !strings.HasPrefix(tl.Name, "simulator_") {
			names = append(names, tl.Name)
		}
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "artifact_publish,session_get,sessions_list" {
		t.Fatalf("tools = %v", names)
	}
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "sessions_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if text := r.Content[0].(*mcp.TextContent).Text; r.IsError || !strings.Contains(text, string(sess.ID)) {
		t.Fatalf("sessions_list = %+v", r)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "session_get", Arguments: map[string]any{"id": "nope-nope"}})
	if err != nil || !r.IsError {
		t.Fatalf("session_get of a missing session = %+v, %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(h, "notes.md"), []byte("# Notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "artifact_publish", Arguments: map[string]any{"name": "notes", "entry": "notes.md"}})
	if err != nil || r.IsError || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, `"kind":"markdown","version":1`) {
		t.Fatalf("artifact_publish = %+v, %v", r, err)
	}

	for name, token := range map[string]string{"no token": "", "wrong token": "bad-token"} {
		t.Run(name, func(t *testing.T) {
			cmd := mcpCommand(h, token)
			var errb bytes.Buffer
			cmd.Stderr = &errb
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 1 || !strings.Contains(errb.String(), "hoplet mcp:") {
				t.Fatalf("exit %v, stderr %q", err, errb.String())
			}
		})
	}
}

func TestMCPUsage(t *testing.T) {
	for _, args := range [][]string{{"mcp"}, {"mcp", "--home", "x", "extra"}} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
}

// resultClient answers every tool call with one fixed result.
type resultClient struct {
	fakeTools
	result proto.ToolsCallResult
}

func (c resultClient) Call(ctx context.Context, method string, params, result any) error {
	if method != proto.MethodToolsCall {
		return c.fakeTools.Call(ctx, method, params, result)
	}
	b, err := json.Marshal(c.result)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func TestForwardImages(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G'}
	for name, tc := range map[string]struct {
		result    proto.ToolsCallResult
		wantTypes []string
		isErr     bool
	}{
		"text then images": {proto.ToolsCallResult{Text: "{}", Images: []proto.ToolImage{{MIME: "image/png", Data: png}, {MIME: "image/jpeg", Data: png}}}, []string{"text", "image/png", "image/jpeg"}, false},
		"text only":        {proto.ToolsCallResult{Text: "[]"}, []string{"text"}, false},
		"failure":          {proto.ToolsCallResult{Text: "busy", IsError: true}, []string{"text"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := forward(resultClient{result: tc.result}, "sessions_list")(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "sessions_list"}})
			if err != nil || res.IsError != tc.isErr || len(res.Content) != len(tc.wantTypes) {
				t.Fatalf("result = %+v, %v", res, err)
			}
			for i, want := range tc.wantTypes {
				switch c := res.Content[i].(type) {
				case *mcp.TextContent:
					if want != "text" || i != 0 || c.Text != tc.result.Text {
						t.Errorf("content %d = %+v", i, c)
					}
				case *mcp.ImageContent:
					if c.MIMEType != want || string(c.Data) != string(png) {
						t.Errorf("content %d = %+v", i, c)
					}
				default:
					t.Errorf("content %d is %T", i, c)
				}
			}
		})
	}
}

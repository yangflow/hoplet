package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// rpc runs lines through Serve and returns the answers.
func rpc(t *testing.T, call Caller, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, call); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("answer %q: %v", l, err)
		}
		got = append(got, m)
	}
	return got
}

func TestInitializeToolsListAndNotification(t *testing.T) {
	got := rpc(t, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	)
	if len(got) != 3 {
		t.Fatalf("a notification must get no answer; got %d answers", len(got))
	}
	res := got[0]["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("version = %v", res["protocolVersion"])
	}
	list := got[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range list {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "list_machines,list_sessions,read_session,get_config,list_skills,read_skill,read_memory,send_prompt,interrupt_session,start_session,wait_children,collect_child,read_child_diff,cancel_child,request_handoff" {
		t.Fatalf("tools = %v", names)
	}
}

func TestUnknownVersionGetsTheServersOwn(t *testing.T) {
	got := rpc(t, nil, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if got[0]["result"].(map[string]any)["protocolVersion"] != protocolVersion {
		t.Fatalf("%v", got[0])
	}
}

func TestToolsCallAndErrors(t *testing.T) {
	var gotTool string
	var gotArgs string
	call := func(tool string, args json.RawMessage) Reply {
		gotTool, gotArgs = tool, string(args)
		if tool == ToolReadSession {
			return errReply("nope")
		}
		return Reply{Text: "ok"}
	}
	got := rpc(t, call,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sessions","arguments":{"limit":3}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_session","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send_message","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
		`not json`,
	)
	if gotTool != ToolReadSession || gotArgs != "{}" {
		t.Fatalf("call = %s %s", gotTool, gotArgs)
	}
	if text := got[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]; text != "ok" {
		t.Fatalf("text = %v", text)
	}
	if got[1]["result"].(map[string]any)["isError"] != true {
		t.Fatalf("an error reply is isError: %v", got[1])
	}
	if got[2]["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Fatalf("an unknown tool is invalid params: %v", got[2])
	}
	if got[3]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("an unknown method: %v", got[3])
	}
	if got[4]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatalf("a bad line: %v", got[4])
	}
}

func TestOnlyTheReadToolsAreReadTools(t *testing.T) {
	for _, tool := range tools() {
		isAct := tool.Name == ToolSendPrompt || tool.Name == ToolInterruptSession || tool.Name == ToolStartSession || tool.Name == ToolRequestHandoff ||
			tool.Name == ToolCollectChild || tool.Name == ToolCancelChild
		if IsReadTool(tool.Name) == isAct {
			t.Fatalf("%s: read tool = %v", tool.Name, IsReadTool(tool.Name))
		}
		if !IsKnownTool(tool.Name) {
			t.Fatalf("%s is not known", tool.Name)
		}
	}
	for _, n := range []string{"send_message", "handoff_session", ""} {
		if IsReadTool(n) || IsKnownTool(n) {
			t.Fatalf("%q must not be a tool", n)
		}
	}
}

func TestScrubMasksCredentialShapes(t *testing.T) {
	in := "token hpt_abcdefghijklmnop1234 and Bearer abc.def and https://user:pw@host/x?token=zzz and sk-abcdefghijklmnopqrstu AKIAABCDEFGHIJKLMNOP"
	out := scrub(in)
	for _, leak := range []string{"hpt_abcdef", "abc.def", "user:pw", "=zzz", "sk-abcdef", "AKIAABCDEF"} {
		if strings.Contains(out, leak) {
			t.Fatalf("%q leaked in %q", leak, out)
		}
	}
}

// fakeBackend answers tool calls with a fixed text.
type fakeBackend struct {
	mu    sync.Mutex
	calls []string
	text  string
}

func (f *fakeBackend) Call(_ context.Context, sessionID, tool string, _ json.RawMessage) Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionID+":"+tool)
	return Reply{Text: f.text}
}

func shortHome(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "hm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestSocketProtectionsAndNonce(t *testing.T) {
	home := shortHome(t)
	be := &fakeBackend{text: "leak hpt_abcdefghijklmnop1234"}
	srv, err := Listen(home, be)
	if err != nil {
		t.Skipf("no local sockets here: %v", err)
	}
	defer srv.Close()
	if filepath.Base(srv.Path()) != SockName {
		t.Fatalf("path = %s", srv.Path())
	}
	st, err := os.Stat(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v", st.Mode().Perm())
	}
	dst, err := os.Stat(filepath.Dir(srv.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if dst.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", dst.Mode().Perm())
	}

	// An unknown nonce names no session.
	if rep := dial(srv.Path(), "unknown", ToolListSessions, nil); !rep.IsError {
		t.Fatalf("unknown nonce answered: %+v", rep)
	}
	unregister := srv.Register("n1", "sess-1")
	rep := dial(srv.Path(), "n1", ToolListSessions, json.RawMessage(`{}`))
	if rep.IsError || strings.Contains(rep.Text, "hpt_abcdef") {
		t.Fatalf("reply = %+v (a reply is scrubbed)", rep)
	}
	if len(be.calls) != 1 || be.calls[0] != "sess-1:list_sessions" {
		t.Fatalf("calls = %v: the nonce, not the caller, names the session", be.calls)
	}
	// A tool that is not a read tool never reaches the backend.
	if rep := dial(srv.Path(), "n1", "send_message", nil); !rep.IsError || len(be.calls) != 1 {
		t.Fatalf("non-read tool: %+v %v", rep, be.calls)
	}
	unregister()
	if rep := dial(srv.Path(), "n1", ToolListSessions, nil); !rep.IsError {
		t.Fatalf("a released nonce answered: %+v", rep)
	}
}

func TestSocketRejectsOversizedRequest(t *testing.T) {
	home := shortHome(t)
	srv, err := Listen(home, &fakeBackend{text: "x"})
	if err != nil {
		t.Skipf("no local sockets here: %v", err)
	}
	defer srv.Close()
	srv.Register("n", "s")
	c, err := net.Dial("unix", srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		_, _ = c.Write(bytes.Repeat([]byte("a"), maxRequest+10))
		_ = c.(*net.UnixConn).CloseWrite()
	}()
	var rep Reply
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewDecoder(c).Decode(&rep); err != nil || !rep.IsError {
		t.Fatalf("rep = %+v err = %v", rep, err)
	}
}

func TestServerClosesAndRemovesSocket(t *testing.T) {
	home := shortHome(t)
	srv, err := Listen(home, &fakeBackend{})
	if err != nil {
		t.Skipf("no local sockets here: %v", err)
	}
	p := srv.Path()
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("socket remains: %v", err)
	}
}

func TestRunStdioWithoutSetup(t *testing.T) {
	var out bytes.Buffer
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_machines"}}` + "\n"
	if err := RunStdio(strings.NewReader(in), &out, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"isError":true`) {
		t.Fatalf("out = %s", out.String())
	}
}

// fakeCore serves the routes the read tools use, checks the bearer and counts token requests.
type fakeCore struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []string
	tokens   *Tokens
	issued   int
	rejectN  int // reject this many first calls with 401
	lines    func(r *http.Request) any
}

func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	f := &fakeCore{}
	f.tokens = NewTokens(func(requestID, sessionID string) error {
		f.mu.Lock()
		f.issued++
		n := f.issued
		f.mu.Unlock()
		go f.tokens.Deliver(TokenReply{RequestID: requestID, SessionID: sessionID,
			Token: fmt.Sprintf("hpt_%016d", n), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
		return nil
	})
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, r *http.Request, v any) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery+" "+r.Header.Get("Authorization"))
		reject := f.rejectN > 0
		if reject {
			f.rejectN--
		}
		f.mu.Unlock()
		if reject {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"token-invalid"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		write(w, r, map[string]any{"devices": []map[string]any{{
			"deviceId": "d1", "name": "mac", "connected": true, "labels": []string{"secret"},
			"providers": []map[string]any{{"provider": "claude"}},
		}}})
	})
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		write(w, r, map[string]any{"sessions": []map[string]any{
			{"sessionId": "s1", "title": "one", "provider": "claude", "deviceName": "mac", "runtime": map[string]any{"state": "idle"}, "self": true},
			{"sessionId": "s2", "title": "two", "provider": "claude", "deviceName": "mac", "runtime": map[string]any{"state": "ready"}},
		}})
	})
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if f.lines != nil {
			write(w, r, f.lines(r))
			return
		}
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"code":"not-allowed"}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestCoreListsMachinesAndSessions(t *testing.T) {
	f := newFakeCore(t)
	c := NewCore(f.srv.URL, f.tokens)
	rep := c.Call(context.Background(), "s1", ToolListMachines, nil)
	if rep.IsError || !strings.HasPrefix(rep.Text, dataNotice) || !strings.Contains(rep.Text, `"name":"mac"`) {
		t.Fatalf("machines = %+v", rep)
	}
	if strings.Contains(rep.Text, "secret") || strings.Contains(rep.Text, "hpt_") {
		t.Fatalf("machines leak: %s", rep.Text)
	}
	rep = c.Call(context.Background(), "s1", ToolListSessions, json.RawMessage(`{"limit":1}`))
	if rep.IsError || !strings.Contains(rep.Text, `"sessionId":"s1"`) || strings.Contains(rep.Text, `"sessionId":"s2"`) || !strings.Contains(rep.Text, `"total":2`) {
		t.Fatalf("sessions = %+v", rep)
	}
	// The token travels in the header, once for the session.
	if f.issued != 1 || !strings.HasSuffix(f.requests[0], "Bearer hpt_0000000000000001") {
		t.Fatalf("issued %d, requests %v", f.issued, f.requests)
	}
}

func TestCoreRetriesOnceOnA401WithAFreshToken(t *testing.T) {
	f := newFakeCore(t)
	f.rejectN = 1
	rep := NewCore(f.srv.URL, f.tokens).Call(context.Background(), "s1", ToolListMachines, nil)
	if rep.IsError || f.issued != 2 {
		t.Fatalf("rep = %+v issued = %d", rep, f.issued)
	}
	f.rejectN = 5
	rep = NewCore(f.srv.URL, f.tokens).Call(context.Background(), "s1", ToolListMachines, nil)
	if !rep.IsError || !strings.Contains(rep.Text, "token-invalid") || strings.Contains(rep.Text, "hpt_") {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestCoreRefusalCarriesTheCodeOnly(t *testing.T) {
	f := newFakeCore(t)
	rep := NewCore(f.srv.URL, f.tokens).Call(context.Background(), "s1", ToolReadSession, json.RawMessage(`{"sessionId":"s2"}`))
	if !rep.IsError || !strings.Contains(rep.Text, "not-allowed") {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestReadSessionValidatesArgumentsAndPages(t *testing.T) {
	f := newFakeCore(t)
	f.lines = func(r *http.Request) any {
		return map[string]any{"sessionId": "s2", "start": 5, "end": 7, "total": 9, "entries": []any{
			map[string]any{"lineNo": 5, "blocks": []any{map[string]any{"type": "text", "text": "hello hpt_abcdefghijklmnop1234"}}},
			map[string]any{"lineNo": 6, "blocks": []any{}},
		}}
	}
	c := NewCore(f.srv.URL, f.tokens)
	for _, bad := range []string{``, `{}`, `{"sessionId":"../x"}`, `{"sessionId":"a b"}`} {
		if rep := c.Call(context.Background(), "s1", ToolReadSession, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("%q accepted", bad)
		}
	}
	rep := c.Call(context.Background(), "s1", ToolReadSession, json.RawMessage(`{"sessionId":"s2","start":5,"limit":999}`))
	if rep.IsError || !strings.Contains(rep.Text, `"next":7`) || strings.Contains(rep.Text, "hpt_abcdef") {
		t.Fatalf("rep = %+v", rep)
	}
	last := f.requests[len(f.requests)-1]
	if !strings.Contains(last, "start=5") || !strings.Contains(last, "limit=100") {
		t.Fatalf("the limit is capped at the page maximum: %s", last)
	}
	// Defaults: the latest 20 lines.
	_ = c.Call(context.Background(), "s1", ToolReadSession, json.RawMessage(`{"sessionId":"s2"}`))
	last = f.requests[len(f.requests)-1]
	if !strings.Contains(last, "start=-20") || !strings.Contains(last, "limit=20") {
		t.Fatalf("defaults: %s", last)
	}
}

func TestReadSessionResultIsCappedAt32KiB(t *testing.T) {
	f := newFakeCore(t)
	big := strings.Repeat("x", 20<<10)
	f.lines = func(*http.Request) any {
		return map[string]any{"sessionId": "s2", "start": 0, "end": 3, "total": 10, "entries": []any{
			map[string]any{"lineNo": 0, "blocks": []any{map[string]any{"type": "text", "text": big}}},
			map[string]any{"lineNo": 1, "blocks": []any{map[string]any{"type": "text", "text": big}}},
			map[string]any{"lineNo": 2, "blocks": []any{map[string]any{"type": "text", "text": big}}},
		}}
	}
	rep := NewCore(f.srv.URL, f.tokens).Call(context.Background(), "s1", ToolReadSession, json.RawMessage(`{"sessionId":"s2","start":0}`))
	if rep.IsError || len(rep.Text) > MaxResultBytes {
		t.Fatalf("len = %d err = %v", len(rep.Text), rep.IsError)
	}
	if !strings.Contains(rep.Text, `"cut":true`) || !strings.Contains(rep.Text, `"next":1`) {
		t.Fatalf("the page must stop at whole entries and say where to continue: %.200s", rep.Text)
	}
}

func TestOneOversizedEntryIsTruncatedAndMarked(t *testing.T) {
	f := newFakeCore(t)
	huge := strings.Repeat("y", 200<<10)
	f.lines = func(*http.Request) any {
		return map[string]any{"sessionId": "s2", "start": 3, "end": 4, "total": 4, "entries": []any{
			map[string]any{"lineNo": 3, "blocks": []any{map[string]any{"type": "text", "text": huge}}},
		}}
	}
	rep := NewCore(f.srv.URL, f.tokens).Call(context.Background(), "s1", ToolReadSession, json.RawMessage(`{"sessionId":"s2","start":3}`))
	if rep.IsError || len(rep.Text) > MaxResultBytes || !strings.Contains(rep.Text, `"truncated":true`) {
		t.Fatalf("len = %d: %.200s", len(rep.Text), rep.Text)
	}
}

func TestTokensCacheRefreshAndRefusal(t *testing.T) {
	var mu sync.Mutex
	sent := 0
	var tk *Tokens
	reply := TokenReply{Token: "hpt_aaaaaaaaaaaaaaaaaaaa", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
	tk = NewTokens(func(requestID, sessionID string) error {
		mu.Lock()
		sent++
		r := reply
		mu.Unlock()
		r.RequestID, r.SessionID = requestID, sessionID
		go tk.Deliver(r)
		return nil
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if tok, err := tk.Get(ctx, "s"); err != nil || tok == "" {
			t.Fatalf("get: %q %v", tok, err)
		}
	}
	if sent != 1 {
		t.Fatalf("sent = %d: a held token is reused", sent)
	}
	// Near expiry it is asked for again.
	tk.now = func() time.Time { return time.Now().Add(56 * time.Minute) }
	if _, err := tk.Get(ctx, "s"); err != nil || sent != 2 {
		t.Fatalf("refresh: %v sent=%d", err, sent)
	}
	tk.Forget("s")
	mu.Lock()
	reply = TokenReply{Reason: "not-allowed"}
	mu.Unlock()
	tk.now = time.Now
	_, err := tk.Get(ctx, "s")
	if err == nil || !strings.Contains(err.Error(), "not-allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestTokensTimeoutAndDisconnected(t *testing.T) {
	tk := NewTokens(func(string, string) error { return nil })
	tk.wait = 20 * time.Millisecond
	if _, err := tk.Get(context.Background(), "s"); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}
	tk2 := NewTokens(func(string, string) error { return io.ErrClosedPipe })
	if _, err := tk2.Get(context.Background(), "s"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("err = %v", err)
	}
	tk.Deliver(TokenReply{RequestID: "nobody-waits"}) // dropped, no panic
}

// actCore answers the queue and interrupt routes with the status and body it is told to.
func actCore(t *testing.T, status int, body string, retryAfter string) (*Core, *[]string) {
	t.Helper()
	f := newFakeCore(t)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(b))
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewCore(srv.URL, f.tokens), &seen
}

func TestSendPromptQueuesWithTheSessionsToken(t *testing.T) {
	c, seen := actCore(t, 201, `{"items":[]}`, "")
	rep := c.Call(context.Background(), "s1", ToolSendPrompt, json.RawMessage(`{"sessionId":"s2","text":"run the tests"}`))
	if rep.IsError || !strings.Contains(rep.Text, "Queued") {
		t.Fatalf("rep = %+v", rep)
	}
	got := (*seen)[0]
	if !strings.HasPrefix(got, "POST /api/sessions/s2/queue Bearer hpt_0000000000000001 ") ||
		!strings.Contains(got, `"text":"run the tests"`) || !strings.Contains(got, `"id":"mcp-`) {
		t.Fatalf("request = %s", got)
	}
}

func TestSendPromptRefusalsAreStructured(t *testing.T) {
	cases := []struct {
		name, status, body, retry string
		want                      []string
	}{
		{"mode", "403", `{"code":"forbidden","reason":"target-mode-more-permissive"}`, "", []string{"forbidden", "target-mode-more-permissive"}},
		{"loop", "409", `{"code":"loop-limit"}`, "", []string{"loop-limit"}},
		{"rate", "429", `{"code":"rate-limited"}`, "42", []string{"rate-limited", "retry after 42 seconds"}},
	}
	for _, tc := range cases {
		status := 0
		_, _ = fmt.Sscan(tc.status, &status)
		c, _ := actCore(t, status, tc.body, tc.retry)
		rep := c.Call(context.Background(), "s1", ToolSendPrompt, json.RawMessage(`{"sessionId":"s2","text":"hi"}`))
		if !rep.IsError {
			t.Fatalf("%s: not an error: %+v", tc.name, rep)
		}
		for _, w := range tc.want {
			if !strings.Contains(rep.Text, w) {
				t.Fatalf("%s: %q lacks %q", tc.name, rep.Text, w)
			}
		}
		if strings.Contains(rep.Text, "hpt_") {
			t.Fatalf("%s leaks a token: %s", tc.name, rep.Text)
		}
	}
}

func TestSendPromptAndInterruptValidateArguments(t *testing.T) {
	c, seen := actCore(t, 201, `{}`, "")
	for _, bad := range []string{``, `{}`, `{"sessionId":"s2"}`, `{"sessionId":"s2","text":"  "}`, `{"sessionId":"../x","text":"a"}`, `{"sessionId":"s1","text":"a"}`} {
		if rep := c.Call(context.Background(), "s1", ToolSendPrompt, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("send %q accepted", bad)
		}
	}
	for _, bad := range []string{``, `{}`, `{"sessionId":"a b"}`, `{"sessionId":"s1"}`} {
		if rep := c.Call(context.Background(), "s1", ToolInterruptSession, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("interrupt %q accepted", bad)
		}
	}
	long := `{"sessionId":"s2","text":"` + strings.Repeat("x", maxPromptChars+1) + `"}`
	if rep := c.Call(context.Background(), "s1", ToolSendPrompt, json.RawMessage(long)); !rep.IsError {
		t.Fatal("a text over the limit was accepted")
	}
	if len(*seen) != 0 {
		t.Fatalf("an invalid call reached the core: %v", *seen)
	}
}

func TestInterruptSessionPostsAndRelaysRefusals(t *testing.T) {
	c, seen := actCore(t, 200, `{"ok":true}`, "")
	rep := c.Call(context.Background(), "s1", ToolInterruptSession, json.RawMessage(`{"sessionId":"s2"}`))
	if rep.IsError || !strings.HasPrefix((*seen)[0], "POST /api/sessions/s2/interrupt Bearer hpt_") {
		t.Fatalf("rep = %+v seen = %v", rep, *seen)
	}
	c, _ = actCore(t, 403, `{"code":"forbidden","reason":"not-granted"}`, "")
	rep = c.Call(context.Background(), "s1", ToolInterruptSession, json.RawMessage(`{"sessionId":"s2"}`))
	if !rep.IsError || !strings.Contains(rep.Text, "forbidden") || !strings.Contains(rep.Text, "not-granted") {
		t.Fatalf("rep = %+v", rep)
	}
}

// startCore answers the reads start_session makes and records what it POSTs to /api/sessions.
func startCore(t *testing.T, status int, body, retryAfter string) (*Core, *[]string) {
	t.Helper()
	f := newFakeCore(t)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(b))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/sessions/s1":
			_, _ = w.Write([]byte(`{"sessionId":"s1","provider":"claude","projectPath":"/work/app","ownerDeviceId":"d1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/devices":
			_, _ = w.Write([]byte(`{"devices":[{"deviceId":"d1","name":"mac","providers":[{"provider":"claude","capabilities":{"config":[` +
				`{"id":"mode","category":"mode","type":"select","options":[{"value":"plan"},{"value":"default"},{"value":"acceptEdits"}]}]}}]},` +
				`{"deviceId":"d2","name":"box","providers":[{"provider":"claude","capabilities":{"config":[]}}]}]}`))
		case r.Method == http.MethodPost && (r.URL.Path == "/api/sessions" || r.URL.Path == "/api/handoff-requests"):
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return NewCore(srv.URL, f.tokens), &seen
}

func lastPost(seen []string) string {
	for i := len(seen) - 1; i >= 0; i-- {
		if strings.HasPrefix(seen[i], "POST ") {
			return seen[i]
		}
	}
	return ""
}

func TestStartSessionDefaultsToTheOwnMachineAndProject(t *testing.T) {
	c, seen := startCore(t, 200, `{"ok":true,"sessionId":"n1","promptId":"x"}`, "")
	rep := c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(`{"prompt":"fix the build"}`))
	if rep.IsError || !strings.Contains(rep.Text, "Started session n1") {
		t.Fatalf("rep = %+v", rep)
	}
	got := lastPost(*seen)
	for _, w := range []string{"POST /api/sessions Bearer hpt_0000000000000001 ", `"deviceId":"d1"`, `"projectPath":"/work/app"`,
		`"provider":"claude"`, `"text":"fix the build"`, `"promptId":"mcp-`} {
		if !strings.Contains(got, w) {
			t.Fatalf("request %s lacks %s", got, w)
		}
	}
	if strings.Contains(got, `"config"`) || strings.Contains(got, `"worktree"`) {
		t.Fatalf("nothing was asked for: %s", got)
	}
}

func TestStartSessionPassesMachineModeAndWorktree(t *testing.T) {
	c, seen := startCore(t, 200, `{"ok":true,"sessionId":"n2","promptId":"x","worktree":{"path":"/work/app-wt","branch":"feat/x"}}`, "")
	rep := c.Call(context.Background(), "s1", ToolStartSession,
		json.RawMessage(`{"prompt":"go","machine":"mac","permissionMode":"plan","worktree":true,"branch":"feat/x"}`))
	if rep.IsError || !strings.Contains(rep.Text, "n2") || !strings.Contains(rep.Text, "/work/app-wt") || !strings.Contains(rep.Text, "feat/x") {
		t.Fatalf("rep = %+v", rep)
	}
	got := lastPost(*seen)
	if !strings.Contains(got, `"config":{"mode":"plan"}`) || !strings.Contains(got, `"worktree":{"branch":"feat/x"}`) {
		t.Fatalf("request = %s", got)
	}
	// Another machine needs a project.
	rep = c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(`{"prompt":"go","machine":"box"}`))
	if !rep.IsError {
		t.Fatalf("another machine without a project was accepted: %+v", rep)
	}
	rep = c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(`{"prompt":"go","machine":"d2","projectPath":"/srv/x"}`))
	if rep.IsError || !strings.Contains(lastPost(*seen), `"deviceId":"d2"`) {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestStartSessionRefusalsAreStructured(t *testing.T) {
	cases := []struct {
		name, status, body, retry string
		want                      []string
	}{
		{"mode", "403", `{"code":"forbidden","reason":"target-mode-more-permissive"}`, "", []string{"forbidden", "target-mode-more-permissive"}},
		{"loop", "409", `{"code":"loop-limit"}`, "", []string{"loop-limit"}},
		{"rate", "429", `{"code":"rate-limited"}`, "42", []string{"rate-limited", "retry after 42 seconds"}},
	}
	for _, tc := range cases {
		status := 0
		_, _ = fmt.Sscan(tc.status, &status)
		c, _ := startCore(t, status, tc.body, tc.retry)
		rep := c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(`{"prompt":"hi"}`))
		if !rep.IsError {
			t.Fatalf("%s: not an error: %+v", tc.name, rep)
		}
		for _, w := range tc.want {
			if !strings.Contains(rep.Text, w) {
				t.Fatalf("%s: %q lacks %q", tc.name, rep.Text, w)
			}
		}
		if strings.Contains(rep.Text, "hpt_") {
			t.Fatalf("%s leaks a token: %s", tc.name, rep.Text)
		}
	}
}

func TestStartSessionValidatesArguments(t *testing.T) {
	c, seen := startCore(t, 200, `{"ok":true,"sessionId":"n1","promptId":"x"}`, "")
	long := `{"prompt":"` + strings.Repeat("x", maxPromptChars+1) + `"}`
	for _, bad := range []string{``, `{}`, `{"prompt":"  "}`, long, `{"prompt":"a","worktree":false,"branch":"b"}`, `{"prompt":"a","title":"` + strings.Repeat("t", maxTitleChars+1) + `"}`,
		`{"prompt":"a","machine":"nobody"}`, `{"prompt":"a","permissionMode":"bypassPermissions"}`} {
		if rep := c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("start %q accepted", bad)
		}
	}
	if lastPost(*seen) != "" {
		t.Fatalf("an invalid call reached the core: %v", *seen)
	}
}

func TestRequestHandoffAsksTheCoreAndTellsTheModelToFinish(t *testing.T) {
	c, seen := startCore(t, 201, `{"requestId":"r1","sessionId":"s1","state":"pending"}`, "")
	rep := c.Call(context.Background(), "s1", ToolRequestHandoff, json.RawMessage(`{"machine":"box","reason":"needs the GPU"}`))
	if rep.IsError || !strings.Contains(rep.Text, "waiting for the user") || !strings.Contains(rep.Text, "finish your turn") {
		t.Fatalf("rep = %+v", rep)
	}
	got := lastPost(*seen)
	for _, w := range []string{"POST /api/handoff-requests Bearer hpt_", `"targetDeviceId":"d2"`, `"reason":"needs the GPU"`} {
		if !strings.Contains(got, w) {
			t.Fatalf("request %s lacks %s", got, w)
		}
	}
	c, _ = startCore(t, 201, `{"requestId":"r2","sessionId":"s1","state":"approved"}`, "")
	rep = c.Call(context.Background(), "s1", ToolRequestHandoff, json.RawMessage(`{"machine":"d2","reason":"go"}`))
	if rep.IsError || !strings.Contains(rep.Text, "approved automatically") {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestRequestHandoffRefusalsAndBadArguments(t *testing.T) {
	c, _ := startCore(t, 409, `{"code":"target-offline"}`, "")
	rep := c.Call(context.Background(), "s1", ToolRequestHandoff, json.RawMessage(`{"machine":"box","reason":"x"}`))
	if !rep.IsError || !strings.Contains(rep.Text, "target-offline") {
		t.Fatalf("rep = %+v", rep)
	}
	rep = c.Call(context.Background(), "s1", ToolRequestHandoff, json.RawMessage(`{"machine":"nowhere","reason":"x"}`))
	if !rep.IsError || !strings.Contains(rep.Text, "not-found") {
		t.Fatalf("an unknown machine: %+v", rep)
	}
	for _, bad := range []string{`{}`, `{"machine":"box"}`, `{"machine":"box","reason":"  "}`, `{"machine":"box","reason":"` + strings.Repeat("x", 501) + `"}`} {
		if rep := c.Call(context.Background(), "s1", ToolRequestHandoff, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("%.40s was accepted", bad)
		}
	}
}

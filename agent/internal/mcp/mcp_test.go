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
	if strings.Join(names, ",") != "list_machines,list_sessions,read_session" {
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

func TestOnlyReadToolsExist(t *testing.T) {
	for _, tool := range tools() {
		if !IsReadTool(tool.Name) {
			t.Fatalf("%s is not a read tool", tool.Name)
		}
	}
	for _, n := range []string{"send_message", "start_session", "handoff_session", ""} {
		if IsReadTool(n) {
			t.Fatalf("%q must not be a read tool", n)
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

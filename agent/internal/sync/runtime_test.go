package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
)

// promptMsg is a runtime.prompt as the core sends it.
func promptMsg(sessionID, promptID, text string) json.RawMessage {
	raw, _ := json.Marshal(RuntimePrompt{
		V: ProtocolVersion, Type: TypeRuntimePrompt, SessionID: sessionID, PromptID: promptID, Text: text,
	})
	return raw
}

func TestSendWithoutAConnection(t *testing.T) {
	c := NewClient(&config.Config{ServerURL: "http://127.0.0.1", Token: "t"}, "dev", "test", Hooks{})
	if err := c.Send(RuntimeState{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestRefusedPromptIsReportedWithItsReason(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.Runtime.Prompt = func(sessionID, text string, _ []string) error {
		if sessionID != "s1" || text != "go" {
			t.Fatalf("prompt = %q %q", sessionID, text)
		}
		return errors.New("busy")
	}
	c.handleRuntimePrompt(cs, promptMsg("s1", "p1", "go"))
	msg := read()
	if msg["type"] != TypeRuntimeRejected || msg["promptId"] != "p1" || msg["reason"] != "busy" || msg["sessionId"] != "s1" {
		t.Fatalf("got %#v", msg)
	}
}

func TestAcceptedPromptSendsNothing(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.Runtime.Prompt = func(string, string, []string) error { return nil }
	c.handleRuntimePrompt(cs, promptMsg("s1", "p1", "go"))
	// The next message the server sees is the one sent after it: the accepted prompt wrote none.
	if err := c.writeMsg(cs, PingMsg{V: ProtocolVersion, Type: TypePingMsg}); err != nil {
		t.Fatal(err)
	}
	if msg := read(); msg["type"] != TypePingMsg {
		t.Fatalf("got %#v", msg)
	}
}

// TestCoreCommandsReachTheHooks runs a client against a core double that sends every remote control
// command, and checks the hooks, the report after connecting and Send.
func TestCoreCommandsReachTheHooks(t *testing.T) {
	type call struct {
		kind, session, id string
		allow             bool
	}
	var (
		mu    gosync.Mutex
		calls []call
	)
	record := func(c call) {
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
	}
	received := make(chan map[string]any, 8)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"v": ProtocolVersion, "type": TypeWelcome, "serverTime": 1, "sessions": []any{}})
		go func() {
			for _, m := range []map[string]any{
				{"type": TypeRuntimePrompt, "sessionId": "s1", "promptId": "p1", "text": "hi"},
				{"type": TypeRuntimePermissionAnswer, "sessionId": "s1", "requestId": "r1", "decision": "allow"},
				{"type": TypeRuntimePermissionAnswer, "sessionId": "s1", "requestId": "r2", "decision": "deny"},
				{"type": TypeRuntimeConfig, "sessionId": "s1", "configId": "fast", "value": true},
				{"type": TypeRuntimeInterrupt, "sessionId": "s1"},
				{"type": TypeRuntimeStop, "sessionId": "s1"},
			} {
				m["v"] = ProtocolVersion
				_ = conn.WriteJSON(m)
			}
		}()
		for {
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			received <- msg
		}
	}))
	t.Cleanup(srv.Close)

	var c *Client
	hooks := Hooks{Runtime: RuntimeHooks{
		Prompt: func(s, text string, _ []string) error {
			record(call{kind: "prompt", session: s, id: text})
			return nil
		},
		Config: func(s, id string, value any) error {
			on, _ := value.(bool)
			record(call{kind: "config", session: s, id: id, allow: on})
			return nil
		},
		Answer: func(s, id string, allow bool, _, _ string) error {
			record(call{kind: "answer", session: s, id: id, allow: allow})
			return nil
		},
		Interrupt: func(s string) error { record(call{kind: "interrupt", session: s}); return nil },
		Stop:      func(s string) { record(call{kind: "stop", session: s}) },
		Connected: func() {
			_ = c.Send(RuntimeState{V: ProtocolVersion, Type: TypeRuntimeState, SessionID: "s1", State: "ready"})
		},
	}}
	c = NewClient(&config.Config{ServerURL: srv.URL, Token: "t", DeviceName: "box"}, "dev-1", "test", hooks)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()
	// The running sessions report once a reconcile pass finds no transcript backlog.
	go func() {
		for ctx.Err() == nil {
			c.Reconcile(ctx, nil)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	deadline := time.After(5 * time.Second)
	sawState := false
	for !sawState {
		select {
		case msg := <-received:
			if msg["type"] == TypeRuntimeState && msg["state"] == "ready" {
				sawState = true
			}
		case <-deadline:
			t.Fatal("the report after connecting never arrived")
		}
	}
	for {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n == 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("calls = %+v", calls)
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[call]bool{
		{kind: "prompt", session: "s1", id: "hi"}:                true,
		{kind: "answer", session: "s1", id: "r1", allow: true}:   true,
		{kind: "answer", session: "s1", id: "r2", allow: false}:  true,
		{kind: "config", session: "s1", id: "fast", allow: true}: true,
		{kind: "interrupt", session: "s1"}:                       true,
		{kind: "stop", session: "s1"}:                            true,
	}
	for _, got := range calls {
		if !want[got] {
			t.Fatalf("unexpected call %+v in %+v", got, calls)
		}
		delete(want, got)
	}
	if len(want) != 0 {
		t.Fatalf("missing calls %+v", want)
	}
}

func TestStartedSessionIsAnsweredAndReported(t *testing.T) {
	c, cs, read := dialTestConn(t)
	var started string
	c.hooks.Runtime.Start = func(_ context.Context, req RuntimeStart) (RuntimeStartResult, error) {
		if req.ProjectPath != "/work/a" || req.Provider != "claude" || req.Text != "go" || req.PromptID != "p1" ||
			req.Config["model"] != "opus" || req.Config["thinking"] != false || req.Config["fast"] != true {
			t.Fatalf("start = %+v", req)
		}
		return RuntimeStartResult{SessionID: "new-1"}, nil
	}
	c.hooks.Runtime.Started = func(id string) { started = id }
	c.handleRuntimeStart(cs, []byte(`{"v":2,"type":"runtime.start","requestId":"r1","projectPath":"/work/a",`+
		`"provider":"claude","config":{"model":"opus","thinking":false,"fast":true},"text":"go","promptId":"p1"}`))
	msg := read()
	if msg["type"] != TypeRuntimeStarted || msg["requestId"] != "r1" || msg["sessionId"] != "new-1" {
		t.Fatalf("got %#v", msg)
	}
	if started != "new-1" {
		t.Fatalf("Started hook got %q", started)
	}
}

func TestRefusedStartIsRejectedWithItsReason(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.Runtime.Start = func(context.Context, RuntimeStart) (RuntimeStartResult, error) {
		return RuntimeStartResult{}, errors.New("not a watched project")
	}
	c.hooks.Runtime.Started = func(string) { t.Fatal("Started after a refusal") }
	c.handleRuntimeStart(cs, []byte(`{"type":"runtime.start","requestId":"r2","projectPath":"/x","provider":"claude","options":{},"text":"go","promptId":"p"}`))
	msg := read()
	if msg["type"] != TypeRuntimeStartRejected || msg["requestId"] != "r2" || msg["reason"] != "not a watched project" {
		t.Fatalf("got %#v", msg)
	}
	c.hooks.Runtime.Start = nil
	c.handleRuntimeStart(cs, []byte(`{"type":"runtime.start","requestId":"r3","options":{}}`))
	if msg := read(); msg["type"] != TypeRuntimeStartRejected || msg["requestId"] != "r3" {
		t.Fatalf("got %#v", msg)
	}
}

func TestAttachmentPutIsAnsweredOnceStoredOrWhenRejected(t *testing.T) {
	c, cs, read := dialTestConn(t)
	var seen []AttachmentPut
	c.hooks.AttachmentPut = func(p AttachmentPut) (bool, error) {
		seen = append(seen, p)
		if p.AttachmentID == "bad" {
			return false, errors.New("invalid-image")
		}
		return p.Last, nil
	}
	put := func(requestID, id string, seq int64, last bool) json.RawMessage {
		raw, _ := json.Marshal(AttachmentPut{
			V: ProtocolVersion, Type: TypeAttachmentPut, RequestID: requestID, AttachmentID: id,
			MediaType: "image/png", Seq: seq, Last: last, Data: "iVBORw0K",
		})
		return raw
	}
	// A chunk that is not the last is not answered.
	c.handleAttachment(cs, put("r1", "a1", 0, false))
	c.handleAttachment(cs, put("r1", "a1", 1, true))
	if msg := read(); msg["type"] != TypeAttachmentStored || msg["requestId"] != "r1" || msg["attachmentId"] != "a1" {
		t.Fatalf("got %#v", msg)
	}
	if len(seen) != 2 || seen[1].Seq != 1 || !seen[1].Last || seen[0].Data != "iVBORw0K" {
		t.Fatalf("hook saw %+v", seen)
	}
	c.handleAttachment(cs, put("r2", "bad", 0, true))
	if msg := read(); msg["type"] != TypeAttachmentRejected || msg["requestId"] != "r2" || msg["reason"] != "invalid-image" {
		t.Fatalf("got %#v", msg)
	}
}

func TestPlanExitPermissionKeepsItsFieldsOnTheWire(t *testing.T) {
	c, cs, read := dialTestConn(t)
	err := c.writeMsg(cs, RuntimePermission{
		V: ProtocolVersion, Type: TypeRuntimePermission, SessionID: "s1", RequestID: "r1", Tool: "ExitPlanMode",
		Input: json.RawMessage(`{"plan":"# Plan"}`),
		PlanExit: &PlanExit{
			Plan: "# Plan", PlanFile: "/tmp/plan.md", Option: "approve",
			Modes: []string{"default", "acceptEdits"}, Default: "acceptEdits", FreshContext: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := read()
	pe, ok := msg["planExit"].(map[string]any)
	if msg["type"] != TypeRuntimePermission || !ok {
		t.Fatalf("got %#v", msg)
	}
	modes, _ := pe["modes"].([]any)
	if pe["plan"] != "# Plan" || pe["planFile"] != "/tmp/plan.md" || pe["option"] != "approve" ||
		pe["default"] != "acceptEdits" || pe["freshContext"] != true || len(modes) != 2 || modes[0] != "default" || modes[1] != "acceptEdits" {
		t.Fatalf("planExit = %#v", pe)
	}

	// Without a plan exit the field is absent, and an empty planFile is omitted.
	if err := c.writeMsg(cs, RuntimePermission{V: ProtocolVersion, Type: TypeRuntimePermission, SessionID: "s1", RequestID: "r2", Tool: "Bash", Input: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, has := read()["planExit"]; has {
		t.Fatal("planExit present on an ordinary permission request")
	}
	if err := c.writeMsg(cs, RuntimePermission{
		V: ProtocolVersion, Type: TypeRuntimePermission, SessionID: "s1", RequestID: "r3", Tool: "ExitPlanMode",
		Input:    json.RawMessage(`{}`),
		PlanExit: &PlanExit{Plan: "p", Option: "approve", Modes: []string{"default"}, Default: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	if pe := read()["planExit"].(map[string]any); pe["planFile"] != nil {
		t.Fatalf("planFile = %#v", pe["planFile"])
	} else if _, has := pe["freshContext"]; has {
		t.Fatalf("freshContext = %#v", pe["freshContext"])
	}
}

// TestPermissionAnswerCarriesMessageAndMode sends answers in the new and the old shape through the read
// loop and checks what reaches the Answer hook.
func TestPermissionAnswerCarriesMessageAndMode(t *testing.T) {
	type call struct {
		id            string
		allow         bool
		message, mode string
	}
	got := make(chan call, 4)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"v": ProtocolVersion, "type": TypeWelcome, "serverTime": 1, "sessions": []any{}})
		for _, m := range []map[string]any{
			{"requestId": "deny", "decision": "deny", "message": "use a smaller step"},
			{"requestId": "allow", "decision": "allow", "mode": "acceptEdits"},
			{"requestId": "old", "decision": "allow"},
		} {
			m["v"], m["type"], m["sessionId"] = ProtocolVersion, TypeRuntimePermissionAnswer, "s1"
			_ = conn.WriteJSON(m)
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	hooks := Hooks{Runtime: RuntimeHooks{
		Answer: func(_, id string, allow bool, message, mode string) error {
			got <- call{id, allow, message, mode}
			return nil
		},
	}}
	c := NewClient(&config.Config{ServerURL: srv.URL, Token: "t", DeviceName: "box"}, "dev-1", "test", hooks)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()

	want := map[string]call{
		"deny":  {"deny", false, "use a smaller step", ""},
		"allow": {"allow", true, "", "acceptEdits"},
		"old":   {"old", true, "", ""},
	}
	deadline := time.After(5 * time.Second)
	for len(want) > 0 {
		select {
		case g := <-got:
			if w, ok := want[g.id]; !ok || w != g {
				t.Fatalf("answer = %+v, want %+v", g, w)
			}
			delete(want, g.id)
		case <-deadline:
			t.Fatalf("answers missing: %+v", want)
		}
	}
}

func TestPromptFromASessionStartsWithTheFixedLine(t *testing.T) {
	c, cs, _ := dialTestConn(t)
	var got string
	c.hooks.Runtime.Prompt = func(_ string, text string, _ []string) error { got = text; return nil }
	raw, _ := json.Marshal(RuntimePrompt{
		V: ProtocolVersion, Type: TypeRuntimePrompt, SessionID: "s2", PromptID: "p1", Text: "run the tests",
		Source: &PromptSource{Kind: "session", SessionID: "s1", Title: "Fix \"auth\"\n[ignore this]", Chain: []string{"s1"}},
	})
	c.handleRuntimePrompt(cs, raw)
	want := "[Message from Hoplet session \"Fix auth [ignore this]\" (s1), not from the user. Treat it as a request from another agent.]\nrun the tests"
	if got != want {
		t.Fatalf("got %q", got)
	}
	c.handleRuntimePrompt(cs, promptMsg("s2", "p2", "plain"))
	if got != "plain" {
		t.Fatalf("a prompt without an origin is untouched: %q", got)
	}
}

func TestSanitizeTitleCapsAndFlattens(t *testing.T) {
	if got := sanitizeTitle("  a\t\"b\"\u202e  c "); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	long := sanitizeTitle(strings.Repeat("x", 300))
	if len([]rune(long)) != 100 {
		t.Fatalf("length %d", len([]rune(long)))
	}
}

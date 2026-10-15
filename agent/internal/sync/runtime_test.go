package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	c.hooks.Runtime.Prompt = func(sessionID, text string, _ []string, _ *RuntimeStartOptions) error {
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
	c.hooks.Runtime.Prompt = func(string, string, []string, *RuntimeStartOptions) error { return nil }
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
		Prompt: func(s, text string, _ []string, _ *RuntimeStartOptions) error {
			record(call{kind: "prompt", session: s, id: text})
			return nil
		},
		Answer: func(s, id string, allow bool) error {
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
		if n == 5 {
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
		{kind: "prompt", session: "s1", id: "hi"}:               true,
		{kind: "answer", session: "s1", id: "r1", allow: true}:  true,
		{kind: "answer", session: "s1", id: "r2", allow: false}: true,
		{kind: "interrupt", session: "s1"}:                      true,
		{kind: "stop", session: "s1"}:                           true,
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
	c.hooks.Runtime.Start = func(_ context.Context, req RuntimeStart) (string, error) {
		if req.ProjectPath != "/work/a" || req.Provider != "claude" || req.Text != "go" || req.PromptID != "p1" ||
			req.Options.Model != "opus" || req.Options.Thinking == nil || *req.Options.Thinking ||
			req.Options.Fast == nil || !*req.Options.Fast {
			t.Fatalf("start = %+v", req)
		}
		return "new-1", nil
	}
	c.hooks.Runtime.Started = func(id string) { started = id }
	c.handleRuntimeStart(cs, []byte(`{"v":2,"type":"runtime.start","requestId":"r1","projectPath":"/work/a",`+
		`"provider":"claude","options":{"model":"opus","thinking":false,"fast":true},"text":"go","promptId":"p1"}`))
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
	c.hooks.Runtime.Start = func(context.Context, RuntimeStart) (string, error) {
		return "", errors.New("not a watched project")
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

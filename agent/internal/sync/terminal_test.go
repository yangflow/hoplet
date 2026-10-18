package sync

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
)

// serveTerminalCore welcomes the agent, sends the messages after the hello and forwards everything the
// agent writes to got. Closing drop ends the connection.
func serveTerminalCore(t *testing.T, send []map[string]any) (url string, got <-chan map[string]any, drop chan<- struct{}) {
	t.Helper()
	in := make(chan map[string]any, 32)
	d := make(chan struct{})
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"v": ProtocolVersion, "type": TypeWelcome, "serverTime": 1, "sessions": []any{}})
		go func() {
			<-d
			conn.Close()
		}()
		first := true
		for {
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			in <- msg
			if first {
				first = false
				for _, m := range send {
					m["v"] = ProtocolVersion
					_ = conn.WriteJSON(m)
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-d:
		default:
			close(d)
		}
	})
	return srv.URL, in, d
}

func startTerminalClient(t *testing.T, url string, hooks Hooks) <-chan error {
	t.Helper()
	c := NewClient(&config.Config{ServerURL: url, Token: "t", DeviceName: "box"}, "dev-1", "9.9", hooks)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- c.runOnce(ctx) }()
	return done
}

func nextMessage(t *testing.T, got <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no message from the agent")
		return nil
	}
}

func TestHelloAnnouncesTheTerminalCapabilityOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		url, hello := serveCore(t, ProtocolVersion)
		startTerminalClient(t, url, Hooks{Terminal: TerminalHooks{Enabled: enabled}})
		select {
		case msg := <-hello:
			caps, has := msg["capabilities"].(map[string]any)
			if enabled && (!has || caps["terminal"] != true) {
				t.Fatalf("enabled: hello = %#v", msg)
			}
			if !enabled && has {
				t.Fatalf("disabled: hello announces %#v", msg["capabilities"])
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the agent sent no hello")
		}
	}
}

type terminalCalls struct {
	mu    sync.Mutex
	calls []string
	open  TerminalOpen
	input []byte
	cols  int
	rows  int
	acked int64
}

func (r *terminalCalls) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *terminalCalls) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *terminalCalls) hooks(openErr error) TerminalHooks {
	return TerminalHooks{
		Enabled: true,
		Open:    func(o TerminalOpen) error { r.mu.Lock(); r.open = o; r.mu.Unlock(); r.add("open"); return openErr },
		Input:   func(id string, d []byte) { r.mu.Lock(); r.input = d; r.mu.Unlock(); r.add("input " + id) },
		Resize:  func(id string, c, rw int) { r.mu.Lock(); r.cols, r.rows = c, rw; r.mu.Unlock(); r.add("resize " + id) },
		Ack:     func(id string, n int64) { r.mu.Lock(); r.acked = n; r.mu.Unlock(); r.add("ack " + id) },
		Close:   func(id, reason string) { r.add("close " + id + " " + reason) },
	}
}

func terminalMessages() []map[string]any {
	return []map[string]any{
		{"type": TypeTerminalOpen, "terminalId": "t1", "sessionId": "s1", "cols": 100, "rows": 30},
		{"type": TypeTerminalInput, "terminalId": "t1", "data": base64.StdEncoding.EncodeToString([]byte("ls\r"))},
		{"type": TypeTerminalResize, "terminalId": "t1", "cols": 120, "rows": 40},
		{"type": TypeTerminalAck, "terminalId": "t1", "bytes": 4096},
		{"type": TypeTerminalClose, "terminalId": "t1", "reason": "detached"},
	}
}

func TestTerminalMessagesReachTheHooksInOrder(t *testing.T) {
	var rec terminalCalls
	url, got, _ := serveTerminalCore(t, terminalMessages())
	startTerminalClient(t, url, Hooks{Terminal: rec.hooks(nil)})
	nextMessage(t, got) // hello

	waitFor(t, func() bool { return len(rec.snapshot()) == 5 })
	want := []string{"open", "input t1", "resize t1", "ack t1", "close t1 detached"}
	for i, w := range want {
		if rec.snapshot()[i] != w {
			t.Fatalf("calls = %v", rec.snapshot())
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.open.TerminalID != "t1" || rec.open.SessionID != "s1" || rec.open.Cols != 100 || rec.open.Rows != 30 {
		t.Fatalf("open = %+v", rec.open)
	}
	if string(rec.input) != "ls\r" || rec.cols != 120 || rec.rows != 40 || rec.acked != 4096 {
		t.Fatalf("input %q size %dx%d acked %d", rec.input, rec.cols, rec.rows, rec.acked)
	}
}

func TestARefusedOpenIsAnsweredWithItsReason(t *testing.T) {
	var rec terminalCalls
	url, got, _ := serveTerminalCore(t, terminalMessages()[:1])
	startTerminalClient(t, url, Hooks{Terminal: rec.hooks(errors.New(TerminalReasonTurnRunning))})
	nextMessage(t, got) // hello
	msg := nextMessage(t, got)
	if msg["type"] != TypeTerminalRejected || msg["terminalId"] != "t1" || msg["reason"] != TerminalReasonTurnRunning {
		t.Fatalf("answer = %#v", msg)
	}
}

func TestADisabledTerminalRefusesAndIgnoresTheRest(t *testing.T) {
	var rec terminalCalls
	hooks := rec.hooks(nil)
	hooks.Enabled = false
	url, got, _ := serveTerminalCore(t, terminalMessages())
	startTerminalClient(t, url, Hooks{Terminal: hooks})
	nextMessage(t, got) // hello
	msg := nextMessage(t, got)
	if msg["type"] != TypeTerminalRejected || msg["reason"] != TerminalReasonDisabled {
		t.Fatalf("answer = %#v", msg)
	}
	time.Sleep(100 * time.Millisecond)
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("a disabled terminal ran hooks: %v", calls)
	}
}

func TestDisconnectingClosesTheTerminals(t *testing.T) {
	disconnected := make(chan struct{})
	url, got, drop := serveTerminalCore(t, nil)
	done := startTerminalClient(t, url, Hooks{Terminal: TerminalHooks{Enabled: true, Disconnected: func() { close(disconnected) }}})
	nextMessage(t, got) // hello
	close(drop)
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("Disconnected was not called")
	}
	<-done
}

func TestTerminalMessagesAreWrittenOnTheControlLane(t *testing.T) {
	url, got, _ := serveTerminalCore(t, nil)
	hooks := Hooks{Terminal: TerminalHooks{Enabled: true}}
	c := NewClient(&config.Config{ServerURL: url, Token: "t", DeviceName: "box"}, "dev-1", "9.9", hooks)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.runOnce(ctx) }()
	nextMessage(t, got) // hello
	waitFor(t, func() bool { return c.cs.Load() != nil })

	code := 3
	if err := c.SendTerminalOpened("t1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendTerminalOutput("t1", 7, []byte("hi\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := c.SendTerminalClosed("t1", TerminalReasonExit, &code); err != nil {
		t.Fatal(err)
	}
	opened, out, closed := nextMessage(t, got), nextMessage(t, got), nextMessage(t, got)
	if opened["type"] != TypeTerminalOpened || opened["terminalId"] != "t1" {
		t.Fatalf("opened = %#v", opened)
	}
	if out["type"] != TypeTerminalOutput || out["seq"] != float64(7) || out["data"] != base64.StdEncoding.EncodeToString([]byte("hi\r\n")) {
		t.Fatalf("output = %#v", out)
	}
	if closed["type"] != TypeTerminalClosed || closed["reason"] != "exit" || closed["exitCode"] != float64(3) {
		t.Fatalf("closed = %#v", closed)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

// coreDouble welcomes one connection with welcome and hands every message the agent sends to received; send
// writes to the agent.
func coreDouble(t *testing.T, welcome map[string]any) (url string, received chan map[string]any, send chan map[string]any) {
	t.Helper()
	received = make(chan map[string]any, 32)
	send = make(chan map[string]any, 8)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello map[string]any
		if conn.ReadJSON(&hello) != nil {
			return
		}
		welcome["v"], welcome["type"], welcome["serverTime"] = ProtocolVersion, TypeWelcome, 1
		_ = conn.WriteJSON(welcome)
		go func() {
			for m := range send {
				m["v"] = ProtocolVersion
				_ = conn.WriteJSON(m)
			}
		}()
		for {
			var msg map[string]any
			if conn.ReadJSON(&msg) != nil {
				return
			}
			if msg["type"] != TypePingMsg {
				received <- msg
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, received, send
}

func next(t *testing.T, received chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-received:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message from the agent")
		return nil
	}
}

func TestReconnectReplaysTheJournalFromTheCoresMark(t *testing.T) {
	url, received, send := coreDouble(t, map[string]any{"sessions": []any{}, "eventSeq": 1})
	c := NewClient(&config.Config{ServerURL: url, Token: "t", HopletHome: t.TempDir()}, "dev", "test", Hooks{})
	// Two events while offline; the core already applied the first (a lost ack).
	_ = c.SendJournaled(state("a"))
	_ = c.SendJournaled(state("b"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()

	m := next(t, received)
	if m["type"] != TypeRuntimeState || m["sessionId"] != "b" || m["eventSeq"] != float64(2) || m["replayed"] != true {
		t.Fatalf("first message after the welcome: %v", m)
	}
	send <- map[string]any{"type": TypeJournalAck, "eventSeq": 2}
	for c.cs.Load() == nil {
		time.Sleep(time.Millisecond)
	}
	_ = c.SendJournaled(state("c"))
	m = next(t, received)
	if m["sessionId"] != "c" || m["eventSeq"] != float64(3) || m["replayed"] != nil {
		t.Fatalf("live event: %v", m)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(c.journal.pending()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("the ack did not drop the confirmed events: %d pending", len(c.journal.pending()))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReconnectReportsTheLinesWrittenAfterAnotherDeviceTookTheSession(t *testing.T) {
	url, received, _ := coreDouble(t, map[string]any{"eventSeq": 0, "sessions": []any{
		map[string]any{"sessionId": "s1", "byteSize": 5, "ownerDeviceId": "other", "ownerEpoch": 4},
	}})
	c := NewClient(&config.Config{ServerURL: url, Token: "t"}, "dev", "test", Hooks{})
	c.mine["s1"] = true
	c.cursors["s1"] = 5
	c.ownerEpochs["s1"] = 3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()
	for c.cs.Load() == nil {
		time.Sleep(time.Millisecond)
	}
	src := testSource{[]byte("abcd\n123456789\n123456789\n")}
	c.Reconcile(ctx, []provider.Source{src})

	m := next(t, received)
	if m["type"] != TypeSessionDiverged || m["bytes"] != float64(20) || m["lines"] != float64(2) || m["ownerEpoch"] != float64(3) {
		t.Fatalf("divergence: %v", m)
	}
	if m = next(t, received); m["type"] != TypeRuntimeReported {
		t.Fatalf("after the divergence: %v", m)
	}
	c.Reconcile(ctx, []provider.Source{src})
	select {
	case m := <-received:
		t.Fatalf("a session another device owns was synced: %v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestReconnectNamesTheBacklogBeforeTheReport(t *testing.T) {
	url, received, _ := coreDouble(t, map[string]any{"eventSeq": 0, "sessions": []any{}})
	c := NewClient(&config.Config{ServerURL: url, Token: "t"}, "dev", "test", Hooks{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()
	for c.cs.Load() == nil {
		time.Sleep(time.Millisecond)
	}
	go c.Reconcile(ctx, []provider.Source{testSource{[]byte("{}\n")}})
	m := next(t, received)
	sessions, _ := m["sessions"].([]any)
	if m["type"] != TypeDeviceBacklog || len(sessions) != 1 {
		t.Fatalf("backlog: %v", m)
	}
	if m = next(t, received); m["type"] != TypeSessionAppend {
		t.Fatalf("after the backlog: %v", m)
	}
}

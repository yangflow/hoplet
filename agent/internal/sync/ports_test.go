package sync

import (
	"context"
	"reflect"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func TestHelloAnnouncesThePreviewHost(t *testing.T) {
	url, hello := serveCore(t, ProtocolVersion)
	startTerminalClient(t, url, Hooks{PreviewHost: "box.tail.ts.net"})
	select {
	case msg := <-hello:
		if msg["previewHost"] != "box.tail.ts.net" {
			t.Fatalf("hello = %#v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent sent no hello")
	}
}

type watchCalls struct {
	mu   gosync.Mutex
	sets [][]string
}

func (w *watchCalls) watch(ids []string) {
	w.mu.Lock()
	w.sets = append(w.sets, append([]string(nil), ids...))
	w.mu.Unlock()
}

func (w *watchCalls) snapshot() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]string(nil), w.sets...)
}

func TestPortsWatchReachesTheHookWithValidIDsOnly(t *testing.T) {
	var many []any
	for i := 0; i < 70; i++ {
		many = append(many, "s"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	calls := &watchCalls{}
	url, got, _ := serveTerminalCore(t, []map[string]any{
		{"type": TypePortsWatch, "sessionIds": []any{"abc-1", "bad id", "../x", "def_2"}},
		{"type": TypePortsWatch, "sessionIds": many},
		{"type": TypePortsWatch, "sessionIds": []any{}},
	})
	startTerminalClient(t, url, Hooks{Ports: PortsHooks{Watch: calls.watch}})
	nextMessage(t, got) // hello
	waitFor(t, func() bool { return len(calls.snapshot()) >= 3 })
	sets := calls.snapshot()
	if !reflect.DeepEqual(sets[0], []string{"abc-1", "def_2"}) {
		t.Fatalf("first set = %v", sets[0])
	}
	if len(sets[1]) != maxPortsWatch {
		t.Fatalf("the set was not cut: %d", len(sets[1]))
	}
	if len(sets[2]) != 0 {
		t.Fatalf("the empty set was not passed on: %v", sets[2])
	}
}

func TestDisconnectingStopsThePortScan(t *testing.T) {
	calls := &watchCalls{}
	url, got, drop := serveTerminalCore(t, nil)
	done := startTerminalClient(t, url, Hooks{Ports: PortsHooks{Watch: calls.watch}})
	nextMessage(t, got) // hello
	close(drop)
	<-done
	sets := calls.snapshot()
	if len(sets) == 0 || len(sets[len(sets)-1]) != 0 {
		t.Fatalf("Watch(nil) was not called on disconnect: %v", sets)
	}
}

func TestSessionPortsIsWrittenOnTheControlLane(t *testing.T) {
	url, got, _ := serveTerminalCore(t, nil)
	c := NewClient(&config.Config{ServerURL: url, Token: "t", DeviceName: "box"}, "dev-1", "9.9", Hooks{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.runOnce(ctx) }()
	nextMessage(t, got) // hello
	waitFor(t, func() bool { return c.cs.Load() != nil })

	if err := c.SendSessionPorts("s1", []SessionPort{{Port: 5173, Address: "loopback", Process: "node"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendSessionPorts("s2", nil); err != nil {
		t.Fatal(err)
	}
	first, second := nextMessage(t, got), nextMessage(t, got)
	if first["type"] != TypeSessionPorts || first["sessionId"] != "s1" {
		t.Fatalf("first = %#v", first)
	}
	ports, _ := first["ports"].([]any)
	entry, _ := ports[0].(map[string]any)
	if len(ports) != 1 || entry["port"] != float64(5173) || entry["address"] != "loopback" || entry["process"] != "node" || len(entry) != 3 {
		t.Fatalf("ports = %#v", first["ports"])
	}
	if empty, ok := second["ports"].([]any); !ok || len(empty) != 0 {
		t.Fatalf("an empty list must be sent as [], got %#v", second["ports"])
	}
}

package sync

import (
	"testing"
	"time"
)

func TestHelloAnnouncesSessionMcpOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		url, hello := serveCore(t, ProtocolVersion)
		startTerminalClient(t, url, Hooks{SessionMCP: enabled})
		select {
		case msg := <-hello:
			caps, has := msg["capabilities"].(map[string]any)
			if enabled && (!has || caps["sessionMcp"] != true) {
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

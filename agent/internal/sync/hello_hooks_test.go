package sync

import (
	"testing"
	"time"
)

func TestHelloAnnouncesTheScriptHooks(t *testing.T) {
	url, hello := serveCore(t, ProtocolVersion)
	startTerminalClient(t, url, Hooks{ScriptHooks: func() []HookAnnounce {
		return []HookAnnounce{{ID: "notify", Command: "/opt/notify.sh", Events: []string{"*"}, Projects: []string{}, TimeoutMs: 10000, Env: []string{"MY_TOKEN"}, Runs: 2, Failed: 1}}
	}})
	select {
	case msg := <-hello:
		list, _ := msg["hooks"].([]any)
		if len(list) != 1 {
			t.Fatalf("hello = %#v", msg)
		}
		h, _ := list[0].(map[string]any)
		if h["id"] != "notify" || h["timeoutMs"] != float64(10000) || h["runs"] != float64(2) || h["failed"] != float64(1) {
			t.Fatalf("hook = %#v", h)
		}
		if env, _ := h["env"].([]any); len(env) != 1 || env[0] != "MY_TOKEN" {
			t.Fatalf("env = %#v", h["env"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent sent no hello")
	}
}

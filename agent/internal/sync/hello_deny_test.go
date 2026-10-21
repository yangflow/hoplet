package sync

import (
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/rules"
)

func TestHelloAnnouncesThePermissionDeny(t *testing.T) {
	url, hello := serveCore(t, ProtocolVersion)
	deny := rules.Announced([]rules.Rule{{
		ID: "local.no-curl", Tool: "run", Match: rules.Match{Command: "curl"}, Action: rules.Deny, Note: "no curl",
	}})
	startTerminalClient(t, url, Hooks{PermissionDeny: deny})
	select {
	case msg := <-hello:
		list, _ := msg["permissionDeny"].([]any)
		if len(list) != 1 {
			t.Fatalf("hello = %#v", msg)
		}
		r, _ := list[0].(map[string]any)
		if r["id"] != "local.no-curl" || r["scope"] != "agent" || r["action"] != "deny" || r["enabled"] != true || r["builtin"] != false {
			t.Fatalf("rule = %#v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent sent no hello")
	}
}

func TestHelloWithoutPermissionDenyOmitsIt(t *testing.T) {
	url, hello := serveCore(t, ProtocolVersion)
	startTerminalClient(t, url, Hooks{})
	select {
	case msg := <-hello:
		if _, ok := msg["permissionDeny"]; ok {
			t.Fatalf("hello = %#v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent sent no hello")
	}
}

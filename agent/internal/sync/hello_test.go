package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
)

// serveCore accepts one connection, sends welcome with the given protocol version, and hands the
// first message the agent sends to hello.
func serveCore(t *testing.T, welcomeVersion int) (url string, hello <-chan map[string]any) {
	t.Helper()
	got := make(chan map[string]any, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{
			"v": welcomeVersion, "type": TypeWelcome, "serverTime": 1, "sessions": []any{},
		})
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		got <- msg
		// Keep the connection open until the agent goes away.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

func TestHelloCarriesProtocolVersion(t *testing.T) {
	url, hello := serveCore(t, ProtocolVersion)
	c := NewClient(&config.Config{ServerURL: url, Token: "t", DeviceName: "box"}, "dev-1", "9.9", Hooks{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()
	select {
	case msg := <-hello:
		if msg["type"] != TypeDeviceHello || msg["v"] != float64(ProtocolVersion) {
			t.Fatalf("hello = %#v", msg)
		}
		if msg["deviceId"] != "dev-1" || msg["name"] != "box" || msg["version"] != "9.9" {
			t.Fatalf("hello = %#v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent sent no hello")
	}
}

func TestAgentRefusesACoreWithAnotherProtocolVersion(t *testing.T) {
	url, _ := serveCore(t, ProtocolVersion+1)
	c := NewClient(&config.Config{ServerURL: url, Token: "t", DeviceName: "box"}, "dev-1", "test", Hooks{})
	err := c.runOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("runOnce error = %v", err)
	}
}

func TestProtocolVersionMismatchErrorDecodes(t *testing.T) {
	raw := []byte(`{"v":1,"type":"error","code":"protocol-version-mismatch","message":"protocol version 2 is not supported"}`)
	var msg ErrorMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != TypeErrorMsg || msg.Code != CodeProtocolVersionMismatch {
		t.Fatalf("msg = %#v", msg)
	}
}

func TestHelloAnnouncesTheProviders(t *testing.T) {
	url, hello := serveCore(t, ProtocolVersion)
	providers := []ProviderInfo{{
		Provider: "claude",
		Capabilities: RuntimeCapabilities{Permissions: true, Config: []ConfigOption{{
			Type: "select", ID: "model", Name: "Model", Category: "model", CurrentValue: "opus",
			Options: []ConfigValue{{Value: "opus", Name: "opus"}},
		}}},
	}}
	c := NewClient(&config.Config{ServerURL: url, Token: "t", DeviceName: "box"}, "dev-1", "9.9", Hooks{Providers: providers})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()
	select {
	case msg := <-hello:
		list, _ := msg["providers"].([]any)
		if len(list) != 1 {
			t.Fatalf("hello = %#v", msg)
		}
		p, _ := list[0].(map[string]any)
		caps, _ := p["capabilities"].(map[string]any)
		config, _ := caps["config"].([]any)
		first, _ := config[0].(map[string]any)
		if p["provider"] != "claude" || caps["interrupt"] != false || len(config) != 1 || first["id"] != "model" ||
			first["currentValue"] != "opus" {
			t.Fatalf("provider = %#v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent sent no hello")
	}
}

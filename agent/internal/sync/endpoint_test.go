package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func TestWsEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://core:7788":                "ws://core:7788/ws/agent",
		"https://hoplet.example.internal": "wss://hoplet.example.internal/ws/agent",
		"https://example.internal/hoplet": "wss://example.internal/hoplet/ws/agent",
		"http://core:7788/":               "ws://core:7788/ws/agent",
		"ws://core:7788/ws/agent":         "ws://core:7788/ws/agent",
	}
	for in, want := range cases {
		got, err := wsEndpoint(in)
		if err != nil || got != want {
			t.Errorf("wsEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := wsEndpoint("ftp://core"); err == nil {
		t.Error("an unsupported scheme should be refused")
	}
}

// The agent dials /ws/agent and presents its device token in the Authorization header, never in the URL.
func TestAgentDialsTheAgentSocketWithTheTokenInTheHeader(t *testing.T) {
	type dial struct {
		path, query, auth string
	}
	seen := make(chan dial, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- dial{r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer srv.Close()

	c := NewClient(&config.Config{ServerURL: srv.URL, Token: "device-secret", DeviceName: "box"}, "dev-1", "test", Hooks{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.runOnce(ctx) }()
	select {
	case got := <-seen:
		if got.path != "/ws/agent" || got.query != "" || got.auth != "Bearer device-secret" {
			t.Fatalf("dial = %#v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent did not dial")
	}
}

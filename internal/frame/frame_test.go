package frame

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestWebSocket(t *testing.T) {
	ctx := t.Context()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		c := WebSocket(ws)
		defer c.Close()
		for {
			p, err := c.ReadFrame(r.Context())
			if err != nil {
				return
			}
			if err := c.WriteFrame(r.Context(), append([]byte("echo "), p...)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := WebSocket(ws)
	read := make(chan string, 2)
	go func() {
		defer close(read)
		for {
			p, err := c.ReadFrame(ctx)
			if err != nil {
				return
			}
			read <- string(p)
		}
	}()
	if err := c.WriteFrame(ctx, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if got := <-read; got != "echo one" {
		t.Fatalf("read %q", got)
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, ok := <-read; ok {
		t.Fatal("read after shutdown")
	}
	if err := c.WriteFrame(ctx, []byte("late")); err == nil {
		t.Fatal("write after shutdown succeeded")
	}
	_ = c.Close()
}

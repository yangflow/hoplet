package noise

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// wsPair connects a client WSConn to a server-side one over loopback.
func wsPair(t *testing.T) (client *WSConn, server *websocket.Conn) {
	t.Helper()
	got := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		got <- ws
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	server = <-got
	t.Cleanup(func() { _ = server.CloseNow() })
	return NewWSConn(ws), server
}

func TestWSConnMessages(t *testing.T) {
	ctx := t.Context()
	c, s := wsPair(t)
	if err := c.WriteMessage(ctx, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if typ, p, err := s.Read(ctx); err != nil || typ != websocket.MessageBinary || string(p) != "hello" {
		t.Fatalf("server read %v %q %v", typ, p, err)
	}
	big := bytes.Repeat([]byte{1}, wsReadLimit)
	if err := s.Write(ctx, websocket.MessageBinary, big); err != nil {
		t.Fatal(err)
	}
	if p, err := c.ReadMessage(ctx); err != nil || !bytes.Equal(p, big) {
		t.Fatalf("read at the limit: %d %v", len(p), err)
	}
	if err := s.Write(ctx, websocket.MessageText, []byte("text")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadMessage(ctx); !errors.Is(err, ErrFrame) {
		t.Fatalf("text message: %v", err)
	}
}

func TestWSConnTooLarge(t *testing.T) {
	ctx := t.Context()
	c, s := wsPair(t)
	go func() { _ = s.Write(ctx, websocket.MessageBinary, make([]byte, wsReadLimit+1)) }()
	if _, err := c.ReadMessage(ctx); err == nil {
		t.Fatal("read over the limit succeeded")
	}
}

func TestWSConnContext(t *testing.T) {
	c, _ := wsPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := c.ReadMessage(ctx); done <- err }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
}

// TestWSConnHandshake runs a pairing handshake and a frame over WebSocket.
func TestWSConnHandshake(t *testing.T) {
	ctx := t.Context()
	d, a, _, _ := configs(t, KindPair)
	c, s := wsPair(t)
	type result struct {
		ch  *Channel
		err error
	}
	acc := make(chan result, 1)
	go func() {
		ch, _, err := Accept(ctx, NewWSConn(s), a)
		acc <- result{ch, err}
	}()
	dch, err := Dial(ctx, c, d)
	if err != nil {
		t.Fatal(err)
	}
	defer dch.Close()
	r := <-acc
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer r.ch.Close()
	big := bytes.Repeat([]byte("x"), 3*maxPiece)
	go func() { _ = dch.WriteFrame(ctx, big) }()
	if p, err := r.ch.ReadFrame(ctx); err != nil || !bytes.Equal(p, big) {
		t.Fatalf("frame over websocket: %d %v", len(p), err)
	}
}

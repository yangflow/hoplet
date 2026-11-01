package hermes

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeGateway is a Hermes gateway: a `hermes` script answering `gateway
// ensure`, a control socket minting tickets and a WebSocket endpoint.
type fakeGateway struct {
	t     *testing.T
	home  string
	bin   string
	conns chan *websocket.Conn
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	home, err := os.MkdirTemp("", "hg") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	g := &fakeGateway{t: t, home: home, conns: make(chan *websocket.Conn, 4)}
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offered := strings.Split(strings.ReplaceAll(r.Header.Get("Sec-WebSocket-Protocol"), " ", ""), ",")
		if r.Header.Get("Origin") != "" || !slices.Contains(offered, ticketPrefix+"t1") {
			http.Error(w, "refused", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}})
		if err != nil {
			return
		}
		g.conns <- c
		<-stop // a hijacked request's context never ends
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) }) // runs first
	ln, err := net.Listen("unix", filepath.Join(home, "gateway.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(c).ReadBytes('\n')
			_, _ = c.Write([]byte(`{"ok":true,"protocol":1,"id":1,"result":{"ticket":"t1","instance_id":"i1","profile_id":"p1"}}` + "\n"))
			c.Close()
		}
	}()
	ensure, _ := json.Marshal(map[string]any{"state": "ready", "endpoint": map[string]any{
		"profile_id": "p1", "instance_id": "i1", "runtime_protocol": 1, "api_origin": srv.URL,
		"capabilities": []string{"session-authority-v1"}, "control_home": home,
	}})
	g.bin = filepath.Join(home, "hermes")
	script := "#!/bin/sh\necho '" + string(ensure) + "'\n"
	if err := os.WriteFile(g.bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *fakeGateway) accept() *websocket.Conn {
	g.t.Helper()
	select {
	case c := <-g.conns:
		return c
	case <-time.After(10 * time.Second):
		g.t.Fatal("the bridge never connected")
		return nil
	}
}

func readFrame(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func writeFrame(t *testing.T, c *websocket.Conn, frame string) {
	t.Helper()
	if err := c.Write(context.Background(), websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
}

// startBridge runs a bridge against g and returns its input, its output
// lines and a stop function that waits for it to return.
func startBridge(t *testing.T, g *fakeGateway, heartbeat time.Duration) (io.Writer, <-chan string, func() error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Bridge(ctx, BridgeConfig{
			Hermes: g.bin, Env: []string{"HOME=" + g.home}, In: inR, Out: outW,
			Backoff: time.Millisecond, Heartbeat: heartbeat,
		})
		outW.Close()
	}()
	var once sync.Once
	var stopErr error
	stop := func() error {
		once.Do(func() {
			cancel()
			stopErr = <-done
			inW.Close()
		})
		return stopErr
	}
	t.Cleanup(func() { _ = stop() })
	return inW, lines, stop
}

func nextLine(t *testing.T, lines <-chan string) map[string]any {
	t.Helper()
	select {
	case l, ok := <-lines:
		if !ok {
			t.Fatal("the bridge output ended")
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("no line from the bridge")
		return nil
	}
}

func TestBridgeRelaysBothWays(t *testing.T) {
	g := newFakeGateway(t)
	in, lines, _ := startBridge(t, g, time.Hour)
	c := g.accept()
	if l := nextLine(t, lines); l["hoplet_bridge"] != BridgeConnected || l["instance_id"] != "i1" {
		t.Fatalf("first line %v", l)
	}
	writeFrame(t, c, `{"jsonrpc":"2.0", "method":"event", "params":{"type":"message.start","session_id":"s","payload":{}}}`)
	if l := nextLine(t, lines); l["method"] != "event" {
		t.Fatalf("relayed %v", l)
	}
	if _, err := io.WriteString(in, `{"jsonrpc":"2.0","id":"r1","method":"ping","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if f := readFrame(t, c); f["id"] != "r1" {
		t.Fatalf("sent %v", f)
	}
}

func TestBridgeKeepsHeartbeatsToItself(t *testing.T) {
	g := newFakeGateway(t)
	_, lines, _ := startBridge(t, g, 10*time.Millisecond)
	c := g.accept()
	nextLine(t, lines) // connected
	ping := readFrame(t, c)
	if id, _ := ping["id"].(string); !strings.HasPrefix(id, heartbeatID) || ping["method"] != "ping" {
		t.Fatalf("heartbeat %v", ping)
	}
	writeFrame(t, c, `{"jsonrpc":"2.0","id":"`+ping["id"].(string)+`","result":{"pong":true}}`)
	writeFrame(t, c, `{"jsonrpc":"2.0","method":"event","params":{"type":"after"}}`)
	if l := nextLine(t, lines); l["method"] != "event" {
		t.Fatalf("a heartbeat answer was relayed: %v", l)
	}
}

func TestBridgeReconnects(t *testing.T) {
	g := newFakeGateway(t)
	_, lines, _ := startBridge(t, g, time.Hour)
	c := g.accept()
	nextLine(t, lines)
	_ = c.CloseNow()
	if l := nextLine(t, lines); l["hoplet_bridge"] != BridgeDisconnected {
		t.Fatalf("after a drop %v", l)
	}
	g.accept()
	if l := nextLine(t, lines); l["hoplet_bridge"] != BridgeConnected {
		t.Fatalf("after reconnecting %v", l)
	}
}

func TestBridgeInterruptsTheRunningTurnWhenStopped(t *testing.T) {
	g := newFakeGateway(t)
	_, lines, stop := startBridge(t, g, time.Hour)
	c := g.accept()
	nextLine(t, lines)
	writeFrame(t, c, `{"jsonrpc":"2.0","method":"event","params":{"type":"message.start","session_id":"s1","execution_generation":3,"payload":{}}}`)
	nextLine(t, lines)
	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	f := readFrame(t, c)
	params, _ := f["params"].(map[string]any)
	if f["method"] != "session.interrupt" || params["session_id"] != "s1" || params["execution_generation"] != float64(3) {
		t.Fatalf("on stop %v", f)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestBridgeEndsWithItsInput(t *testing.T) {
	g := newFakeGateway(t)
	in, lines, stop := startBridge(t, g, time.Hour)
	g.accept()
	nextLine(t, lines)
	in.(io.Closer).Close()
	for range lines {
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

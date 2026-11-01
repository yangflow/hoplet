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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	mu   sync.Mutex
	all  []*websocket.Conn // every accepted connection, closed on cleanup
	gate chan struct{}     // when set, upgrades wait for it to close
}

// holdUpgrades makes the next upgrades wait until the returned function runs.
func (g *fakeGateway) holdUpgrades() (open func()) {
	gate := make(chan struct{})
	g.mu.Lock()
	g.gate = gate
	g.mu.Unlock()
	return func() { close(gate) }
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
		g.mu.Lock()
		gate := g.gate
		g.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-stop:
				return
			}
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}})
		if err != nil {
			return
		}
		g.mu.Lock()
		g.all = append(g.all, c)
		g.mu.Unlock()
		select {
		case g.conns <- c:
		case <-stop:
			return
		}
		<-stop // a hijacked request's context never ends
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) }) // runs after the connections close
	t.Cleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, c := range g.all {
			_ = c.CloseNow() // ends its read goroutine
		}
	})
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
	// The server answers every ping, so liveness never trips.
	pings := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var f map[string]any
			if json.Unmarshal(b, &f) != nil || f["method"] != "ping" {
				continue
			}
			id, _ := f["id"].(string)
			if !strings.HasPrefix(id, heartbeatID) {
				continue
			}
			select {
			case pings <- id:
			default:
			}
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"pong":true}}`))
		}
	}()
	<-pings // at least one answered heartbeat has gone by
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

func TestBridgeSendsQueuedLinesWhenInputEnds(t *testing.T) {
	g := newFakeGateway(t)
	in, lines, stop := startBridge(t, g, time.Hour)
	c := g.accept()
	nextLine(t, lines)
	for _, id := range []string{"a", "b"} {
		if _, err := io.WriteString(in, `{"jsonrpc":"2.0","id":"`+id+`","method":"ping","params":{}}`+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	in.(io.Closer).Close()
	for _, want := range []string{"a", "b"} {
		if f := readFrame(t, c); f["id"] != want {
			t.Fatalf("want %s, got %v", want, f)
		}
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeDoesNotCarryLinesAcrossAReconnect(t *testing.T) {
	g := newFakeGateway(t)
	in, lines, _ := startBridge(t, g, time.Hour)
	c := g.accept()
	nextLine(t, lines)
	open := g.holdUpgrades() // the bridge cannot report "connected" yet
	_ = c.CloseNow()
	if l := nextLine(t, lines); l["hoplet_bridge"] != BridgeDisconnected {
		t.Fatalf("after a drop %v", l)
	}
	// Two writes: when the second returns, the scanner has handed the first
	// line to the queue. The second may still be on its way, so only the
	// first is asserted on; both predate the connection.
	for _, id := range []string{"old1", "old2"} {
		if _, err := io.WriteString(in, `{"jsonrpc":"2.0","id":"`+id+`","method":"ping","params":{}}`+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	open()
	c2 := g.accept()
	if l := nextLine(t, lines); l["hoplet_bridge"] != BridgeConnected {
		t.Fatalf("after reconnecting %v", l)
	}
	if _, err := io.WriteString(in, `{"jsonrpc":"2.0","id":"fresh","method":"ping","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	for {
		f := readFrame(t, c2)
		if f["id"] == "old1" {
			t.Fatal("a line from before the reconnect was delivered")
		}
		if f["id"] == "fresh" {
			break
		}
	}
}

func TestBridgeDropsAGatewayThatStopsAnswering(t *testing.T) {
	g := newFakeGateway(t)
	_, lines, _ := startBridge(t, g, 10*time.Millisecond)
	g.accept() // never reads, never answers
	nextLine(t, lines)
	l := nextLine(t, lines)
	if l["hoplet_bridge"] != BridgeDisconnected || !strings.Contains(l["error"].(string), "stopped answering") {
		t.Fatalf("liveness %v", l)
	}
}

// fakeEnsureBin writes a `hermes` script that prints out and exits with code.
func fakeEnsureBin(t *testing.T, out string, code int) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hermes")
	script := "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestEnsure(t *testing.T) {
	endpoint := func(origin string, proto int) map[string]any {
		return map[string]any{"profile_id": "p1", "instance_id": "i1", "runtime_protocol": proto, "api_origin": origin, "control_home": "/x"}
	}
	tests := []struct {
		name    string
		out     map[string]any
		code    int
		wantErr string
	}{
		{"ready", map[string]any{"state": "ready", "endpoint": endpoint("http://127.0.0.1:9", 1)}, 0, ""},
		{"not loopback", map[string]any{"state": "ready", "endpoint": endpoint("http://example.com:9", 1)}, 0, "not loopback"},
		{"not ready", map[string]any{"state": "starting", "reason_code": "booting"}, 0, "starting"},
		{"wrong protocol", map[string]any{"state": "ready", "endpoint": endpoint("http://127.0.0.1:9", 2)}, 0, "protocol 2"},
		{"failed exit", map[string]any{"state": "ready", "endpoint": endpoint("http://127.0.0.1:9", 1)}, 1, "exit status 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _ := json.Marshal(tt.out)
			ep, err := Ensure(context.Background(), fakeEnsureBin(t, string(out), tt.code), []string{"HOME=/nonexistent"})
			if tt.wantErr == "" {
				if err != nil || ep.InstanceID != "i1" {
					t.Fatalf("got %+v, %v", ep, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// controlServer answers every control request with resp and returns the
// Hermes home it listens in.
func controlServer(t *testing.T, resp string) string {
	t.Helper()
	home, err := os.MkdirTemp("", "hc") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
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
			_, _ = c.Write([]byte(resp + "\n"))
			c.Close()
		}
	}()
	return home
}

func TestTicketRefusals(t *testing.T) {
	tests := []struct{ name, resp string }{
		{"instance mismatch", `{"ok":true,"result":{"ticket":"t","instance_id":"other","profile_id":"p1"}}`},
		{"profile mismatch", `{"ok":true,"result":{"ticket":"t","instance_id":"i1","profile_id":"other"}}`},
		{"refused", `{"ok":false,"error":{"code":"denied"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := Endpoint{ProfileID: "p1", InstanceID: "i1", ControlHome: controlServer(t, tt.resp)}
			if tk, err := Ticket(context.Background(), ep); err == nil {
				t.Fatalf("got ticket %q", tk)
			}
		})
	}
}

func TestDialDoesNotFollowRedirects(t *testing.T) {
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/ws", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	ep := Endpoint{
		ProfileID: "p1", InstanceID: "i1", APIOrigin: redirect.URL,
		ControlHome: controlServer(t, `{"ok":true,"result":{"ticket":"t1","instance_id":"i1","profile_id":"p1"}}`),
	}
	if c, err := Dial(context.Background(), ep); err == nil {
		_ = c.CloseNow()
		t.Fatal("Dial followed a redirect")
	}
	if hit.Load() {
		t.Fatal("the redirect target was contacted")
	}
}

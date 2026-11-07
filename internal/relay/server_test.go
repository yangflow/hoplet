package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/testnet"
	"github.com/yangflow/hoplet/proto"
)

// logs records the relay's log messages for tests to wait on.
type logs struct {
	mu      sync.Mutex
	msgs    []string
	changed chan struct{}
}

func newLogs() *logs { return &logs{changed: make(chan struct{})} }

func (l *logs) Enabled(context.Context, slog.Level) bool { return true }
func (l *logs) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logs) WithGroup(string) slog.Handler            { return l }

func (l *logs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, r.Message)
	close(l.changed)
	l.changed = make(chan struct{})
	return nil
}

// wait returns once msg has been logged n times.
func (l *logs) wait(t *testing.T, msg string, n int) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		l.mu.Lock()
		got, changed := 0, l.changed
		for _, m := range l.msgs {
			if m == msg {
				got++
			}
		}
		l.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("waited for log %q ×%d", msg, n)
		}
	}
}

// recorder keeps every byte the relay reads.
type recorder struct {
	net.Listener
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *recorder) Accept() (net.Conn, error) {
	c, err := r.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &recConn{Conn: c, r: r}, nil
}

func (r *recorder) contains(s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Contains(r.buf.Bytes(), []byte(s))
}

type recConn struct {
	net.Conn
	r *recorder
}

func (c *recConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.r.mu.Lock()
	c.r.buf.Write(p[:n])
	c.r.mu.Unlock()
	return n, err
}

type testRelay struct {
	url  string
	dir  string
	logs *logs
	rec  *recorder
	opts *websocket.DialOptions
}

// startRelay runs a relay on a loopback port until the test ends; with
// useTLS it serves a test certificate the returned dial options trust.
func startRelay(t *testing.T, lim limits, useTLS bool) *testRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &testRelay{url: "ws://" + ln.Addr().String(), dir: t.TempDir(), logs: newLogs(), rec: &recorder{Listener: ln}}
	var conf *tls.Config
	if useTLS {
		c, pool := testnet.TLS(t)
		conf = c
		r.url = "wss://" + ln.Addr().String()
		r.opts = &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}
	}
	s := &Server{Dir: r.dir, Logger: slog.New(r.logs), lim: lim}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, r.rec, conf) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return r
}

func newID(t *testing.T) noise.Identity {
	t.Helper()
	id, err := noise.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *testRelay) allow(t *testing.T, id noise.Identity) {
	t.Helper()
	if err := Allow(r.dir, id.ID()); err != nil {
		t.Fatal(err)
	}
}

func timeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func readMessage(ctx context.Context, ch *noise.Channel) (Message, error) {
	b, err := ch.ReadFrame(ctx)
	if err != nil {
		return Message{}, err
	}
	var m Message
	err = json.Unmarshal(b, &m)
	return m, err
}

// register opens a control connection for id and waits for registered.
func (r *testRelay) register(ctx context.Context, id noise.Identity) (*noise.Channel, error) {
	ws, _, err := websocket.Dial(ctx, r.url+proto.RelayHostPath, r.opts)
	if err != nil {
		return nil, err
	}
	ch, err := noise.Dial(ctx, noise.NewWSConn(ws), noise.DialConfig{Identity: id, Kind: noise.KindRelay, MaxFrame: controlMaxFrame})
	if err != nil {
		return nil, err
	}
	m, err := readMessage(ctx, ch)
	if err == nil && m.Type != TypeRegistered {
		err = fmt.Errorf("first message %+v", m)
	}
	if err != nil {
		_ = ch.Close()
		return nil, err
	}
	return ch, nil
}

func (r *testRelay) mustRegister(t *testing.T, id noise.Identity) *noise.Channel {
	t.Helper()
	ch, err := r.register(timeout(t), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

// echo answers every stream announced on ch as the host id would: a
// Noise responder that sends each frame back.
func (r *testRelay) echo(t *testing.T, ch *noise.Channel, id noise.Identity) {
	ctx := t.Context()
	go func() {
		for {
			m, err := readMessage(ctx, ch)
			if err != nil {
				return
			}
			go func() {
				ws, _, err := websocket.Dial(ctx, r.url+proto.RelayAcceptPath+"?stream="+m.Stream, r.opts)
				if err != nil {
					return
				}
				sc, _, err := noise.Accept(ctx, noise.NewWSConn(ws), noise.AcceptConfig{Identity: id, Kinds: []noise.Kind{noise.KindDaily}})
				if err != nil {
					return
				}
				defer sc.Close()
				for {
					f, err := sc.ReadFrame(ctx)
					if err != nil || sc.WriteFrame(ctx, f) != nil {
						return
					}
				}
			}()
		}
	}()
}

// dialStream opens a raw client connection to the host with ID id.
func (r *testRelay) dialStream(ctx context.Context, id noise.HostID, header http.Header) (*websocket.Conn, int, error) {
	opts := &websocket.DialOptions{HTTPHeader: header}
	if r.opts != nil {
		opts.HTTPClient = r.opts.HTTPClient
	}
	ws, resp, err := websocket.Dial(ctx, r.url+proto.RelayConnectPath+"?host="+string(id), opts)
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	return ws, code, err
}

// connect reaches host through the relay as a paired device would.
func (r *testRelay) connect(t *testing.T, host noise.Identity) *noise.Channel {
	t.Helper()
	ctx := timeout(t)
	ws, _, err := r.dialStream(ctx, host.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := noise.Dial(ctx, noise.NewWSConn(ws), noise.DialConfig{Identity: newID(t), Kind: noise.KindDaily, PeerStatic: host.Public()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

func roundTrip(t *testing.T, ch *noise.Channel, frames ...[]byte) {
	t.Helper()
	ctx := timeout(t)
	for _, f := range frames {
		if err := ch.WriteFrame(ctx, f); err != nil {
			t.Fatal(err)
		}
		got, err := ch.ReadFrame(ctx)
		if err != nil || !bytes.Equal(got, f) {
			t.Fatalf("echo of %d bytes: %d bytes, %v", len(f), len(got), err)
		}
	}
}

func TestStreamCarriesOnlyCiphertext(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", useTLS), func(t *testing.T) {
			r := startRelay(t, limits{}, useTLS)
			host := newID(t)
			r.allow(t, host)
			r.echo(t, r.mustRegister(t, host), host)
			ch := r.connect(t, host)
			const marker = "plaintext-marker-7f3a"
			big := bytes.Repeat([]byte(marker), 100<<10/len(marker)) // more than one Noise message
			roundTrip(t, ch, []byte("hello "+marker), big, []byte("again"))
			if !useTLS && r.rec.contains(marker) {
				t.Fatal("the relay read the plaintext")
			}
			_ = ch.Close()
			r.logs.wait(t, "stream ended", 1)
		})
	}
}

func TestRegistration(t *testing.T) {
	r := startRelay(t, limits{}, false)
	host := newID(t)
	if _, err := r.register(timeout(t), host); err == nil {
		t.Fatal("registered a host outside the allowlist")
	}
	r.logs.wait(t, "registration refused", 1)

	r.allow(t, host)
	r.echo(t, r.mustRegister(t, host), host)
	roundTrip(t, r.connect(t, host), []byte("hi"))

	if err := Deny(r.dir, host.ID()); err != nil {
		t.Fatal(err)
	}
	if _, code, err := r.dialStream(timeout(t), host.ID(), nil); err == nil || code != http.StatusNotFound {
		t.Fatalf("connect to a denied host: %d %v", code, err)
	}
	if _, err := r.register(timeout(t), host); err == nil {
		t.Fatal("registered a denied host")
	}
}

func TestNewRegistrationReplacesTheOld(t *testing.T) {
	r := startRelay(t, limits{}, false)
	host := newID(t)
	r.allow(t, host)
	old := r.mustRegister(t, host)
	cur := r.mustRegister(t, host)
	if _, err := old.ReadFrame(timeout(t)); err == nil {
		t.Fatal("the old control connection still reads")
	}
	r.echo(t, cur, host)
	roundTrip(t, r.connect(t, host), []byte("hi"))
}

func TestRefusedRequests(t *testing.T) {
	r := startRelay(t, limits{}, false)
	host := newID(t)
	r.allow(t, host)
	r.mustRegister(t, host)
	other := newID(t)
	r.allow(t, other) // allowed but not registered

	for name, tc := range map[string]struct {
		id     noise.HostID
		header http.Header
		want   int
	}{
		"unregistered": {other.ID(), nil, http.StatusNotFound},
		"malformed":    {"x", nil, http.StatusNotFound},
		"origin":       {host.ID(), http.Header{"Origin": {"https://example.com"}}, http.StatusForbidden},
	} {
		if _, code, err := r.dialStream(timeout(t), tc.id, tc.header); err == nil || code != tc.want {
			t.Errorf("%s: %d %v, want %d", name, code, err, tc.want)
		}
	}
	resp, err := http.Get(strings.Replace(r.url, "ws:", "http:", 1) + "/v1/other")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other path: %d", resp.StatusCode)
	}
}

// acceptStream dials back for token.
func (r *testRelay) acceptStream(ctx context.Context, token string) (*websocket.Conn, int, error) {
	ws, resp, err := websocket.Dial(ctx, r.url+proto.RelayAcceptPath+"?stream="+token, r.opts)
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	return ws, code, err
}

func TestStreamTokens(t *testing.T) {
	r := startRelay(t, limits{acceptWait: 200 * time.Millisecond}, false)
	host := newID(t)
	r.allow(t, host)
	ctl := r.mustRegister(t, host)
	ctx := timeout(t)

	if _, code, err := r.acceptStream(ctx, "bogus"); err == nil || code != http.StatusNotFound {
		t.Fatalf("unknown token: %d %v", code, err)
	}

	// The host lets the wait run out: the client is closed and the token
	// is spent.
	client, _, err := r.dialStream(ctx, host.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(ctx, ctl)
	if err != nil || m.Type != TypeStream || m.Stream == "" {
		t.Fatalf("stream message %+v %v", m, err)
	}
	if _, _, err := client.Read(ctx); err == nil {
		t.Fatal("the client still reads after the wait")
	}
	r.logs.wait(t, "stream not answered", 1)
	if _, code, err := r.acceptStream(ctx, m.Stream); err == nil || code != http.StatusNotFound {
		t.Fatalf("expired token: %d %v", code, err)
	}

	// A token answers once.
	client, _, err = r.dialStream(ctx, host.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.CloseNow() }()
	if m, err = readMessage(ctx, ctl); err != nil {
		t.Fatal(err)
	}
	back, _, err := r.acceptStream(ctx, m.Stream)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = back.CloseNow() }()
	if _, code, err := r.acceptStream(ctx, m.Stream); err == nil || code != http.StatusNotFound {
		t.Fatalf("used token: %d %v", code, err)
	}
	if err := client.Write(ctx, websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, p, err := back.Read(ctx); err != nil || string(p) != "x" {
		t.Fatalf("forwarded %q %v", p, err)
	}
}

func TestLimits(t *testing.T) {
	for name, lim := range map[string]limits{
		"streams per host": {maxStreams: 1},
		"waiting per IP":   {maxWaiting: 1},
	} {
		t.Run(name, func(t *testing.T) {
			r := startRelay(t, lim, false)
			a, b := newID(t), newID(t)
			r.allow(t, a)
			r.allow(t, b)
			r.mustRegister(t, a)
			r.mustRegister(t, b)
			ctx := timeout(t)
			first, _, err := r.dialStream(ctx, a.ID(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first.CloseNow() }()
			target := a.ID() // the host limit counts per host
			if lim.maxWaiting > 0 {
				target = b.ID() // the IP limit counts across hosts
			}
			if _, code, err := r.dialStream(ctx, target, nil); err == nil || code != http.StatusServiceUnavailable {
				t.Fatalf("over the limit: %d %v", code, err)
			}
		})
	}
}

// rawStream joins a client with a host connection without Noise.
func rawStream(t *testing.T, r *testRelay, ctl *noise.Channel, id noise.HostID) (client, back *websocket.Conn) {
	t.Helper()
	ctx := timeout(t)
	client, _, err := r.dialStream(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(ctx, ctl)
	if err != nil {
		t.Fatal(err)
	}
	if back, _, err = r.acceptStream(ctx, m.Stream); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.CloseNow(); _ = back.CloseNow() })
	return client, back
}

func TestStreamEnds(t *testing.T) {
	r := startRelay(t, limits{}, false)
	host := newID(t)
	r.allow(t, host)
	ctl := r.mustRegister(t, host)
	ctx := timeout(t)
	for name, end := range map[string]func(client, back *websocket.Conn) *websocket.Conn{
		"client closes": func(c, b *websocket.Conn) *websocket.Conn { _ = c.CloseNow(); return b },
		"host closes":   func(c, b *websocket.Conn) *websocket.Conn { _ = b.CloseNow(); return c },
		"text message": func(c, b *websocket.Conn) *websocket.Conn {
			_ = c.Write(ctx, websocket.MessageText, []byte("hi"))
			return b
		},
	} {
		client, back := rawStream(t, r, ctl, host.ID())
		other := end(client, back)
		if _, _, err := other.Read(ctx); err == nil {
			t.Errorf("%s: the other side still reads", name)
		}
	}
}

func TestHeartbeats(t *testing.T) {
	r := startRelay(t, limits{pingEvery: 20 * time.Millisecond, pingWait: 50 * time.Millisecond}, false)
	host := newID(t)
	r.allow(t, host)
	ctl := r.mustRegister(t, host)
	ctx := timeout(t)

	// The host's side reads, so it answers pings; the client never reads
	// and does not: the relay ends the stream.
	_, back := rawStream(t, r, ctl, host.ID())
	if _, _, err := back.Read(ctx); err == nil {
		t.Fatal("a stream outlived an unanswered ping")
	}

	// A control connection that stops reading stops answering too.
	r.logs.wait(t, "host left", 1)
	if _, code, err := r.dialStream(ctx, host.ID(), nil); err == nil || code != http.StatusNotFound {
		t.Fatalf("connect after the host left: %d %v", code, err)
	}
}

func TestServeEndsEveryConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &testRelay{url: "ws://" + ln.Addr().String(), dir: t.TempDir(), logs: newLogs()}
	s := &Server{Dir: r.dir, Logger: slog.New(r.logs)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln, nil) }()
	host := newID(t)
	r.allow(t, host)
	ctl := r.mustRegister(t, host)
	client, back := rawStream(t, r, ctl, host.ID())
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rctx := timeout(t)
	if _, err := ctl.ReadFrame(rctx); err == nil {
		t.Error("the control connection outlived Serve")
	}
	for _, c := range []*websocket.Conn{client, back} {
		if _, _, err := c.Read(rctx); err == nil {
			t.Error("a stream outlived Serve")
		}
	}
}

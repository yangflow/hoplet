package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/relay"
	"github.com/yangflow/hoplet/internal/testnet"
	"github.com/yangflow/hoplet/proto"
)

// logs records log messages for tests to wait on.
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
	timeout := time.After(15 * time.Second)
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

// testRelay is a relay on a loopback port with a test certificate.
type testRelay struct {
	addr, url, dir string
	conf           *tls.Config // the relay's
	trust          *tls.Config // what its clients dial with
	cancel         context.CancelFunc
	done           chan error
}

func startRelay(t *testing.T) *testRelay {
	t.Helper()
	conf, roots := testnet.TLS(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &testRelay{addr: ln.Addr().String(), dir: t.TempDir(), conf: conf, trust: &tls.Config{RootCAs: roots}}
	r.url = "wss://" + r.addr
	r.serve(ln)
	t.Cleanup(func() { r.stop(t) })
	return r
}

func (r *testRelay) serve(ln net.Listener) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan error, 1)
	s := &relay.Server{Dir: r.dir}
	go func() { r.done <- s.Serve(ctx, ln, r.conf) }()
}

func (r *testRelay) stop(t *testing.T) {
	if r.cancel == nil {
		return
	}
	r.cancel()
	if err := <-r.done; err != nil {
		t.Error(err)
	}
	r.cancel = nil
}

// restart stops the relay, ending every connection, and starts it again
// on the same address.
func (r *testRelay) restart(t *testing.T) {
	t.Helper()
	r.stop(t)
	ln, err := net.Listen("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	r.serve(ln)
}

// relayHost is a daemon that registers with a relay and has no direct
// listener.
type relayHost struct {
	*hostB
	id   noise.Identity
	logs *logs
}

// startOnRelay starts a daemon registering with r; allow puts it in the
// relay's allowlist first.
func startOnRelay(t *testing.T, s fake.Script, r *testRelay, allow bool) *relayHost {
	t.Helper()
	h, script := newHome(t, s)
	id, err := noise.EnsureIdentity(h)
	if err != nil {
		t.Fatal(err)
	}
	if allow {
		if err := relay.Allow(r.dir, id.ID()); err != nil {
			t.Fatal(err)
		}
	}
	l := newLogs()
	cfg := testConfig(h, script)
	cfg.Relays, cfg.RelayTLS, cfg.Logger = []string{r.url}, r.trust, slog.New(l)
	cfg.lim = limits{relayRetry: 10 * time.Millisecond, relayRetryMax: 50 * time.Millisecond}
	d := start(t, cfg)
	return &relayHost{hostB: &hostB{d: d, home: h, local: dial(t, h, "cli@b")}, id: id, logs: l}
}

// pairOverRelay pairs a new device with b through the relay.
func (b *relayHost) pairOverRelay(t *testing.T, r *testRelay) (noise.Identity, client.Remote) {
	t.Helper()
	code := b.code(t)
	if len(code.Routes) != 1 || code.Routes[0] != r.url+proto.RelayConnectPath {
		t.Fatalf("routes %q", code.Routes)
	}
	a := newIdentity(t)
	rem := remoteOf(code, a)
	rem.TLS = r.trust
	c, err := client.Pair(timeout(t), rem, code.PSK, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if c.Route() != code.Routes[0] {
		t.Fatalf("route %q", c.Route())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return a, rem
}

func TestRelayPairingAndSessions(t *testing.T) {
	r := startRelay(t)
	b := startOnRelay(t, echoScript, r, true)
	b.logs.wait(t, "registered with relay", 1)

	a, rem := b.pairOverRelay(t, r)
	c := dialRemote(t, rem)
	if c.Hello().HostID != string(b.id.ID()) {
		t.Fatalf("hello %+v", c.Hello())
	}
	sess := create(t, c, b.home)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "hi", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeTurnEnded) == 1 })

	stranger := rem
	stranger.Identity = newIdentity(t)
	if _, err := client.DialRemote(timeout(t), stranger); !errors.Is(err, noise.ErrHandshake) {
		t.Fatalf("unpaired device over the relay: %v", err)
	}

	if err := b.local.RevokeDevice(timeout(t), string(a.ID())); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-timeout(t).Done():
		t.Fatal("the revoked device stayed connected through the relay")
	}
}

func TestRelayRegistrationNeedsTheAllowlist(t *testing.T) {
	r := startRelay(t)
	b := startOnRelay(t, echoScript, r, false)
	b.logs.wait(t, "relay refused the registration or is unreachable", 2)
	if err := relay.Allow(r.dir, b.id.ID()); err != nil {
		t.Fatal(err)
	}
	b.logs.wait(t, "registered with relay", 1)
	_, rem := b.pairOverRelay(t, r)
	dialRemote(t, rem)
}

func TestRelayRestart(t *testing.T) {
	r := startRelay(t)
	b := startOnRelay(t, echoScript, r, true)
	b.logs.wait(t, "registered with relay", 1)
	_, rem := b.pairOverRelay(t, r)
	c := dialRemote(t, rem)

	r.restart(t)
	select {
	case <-c.Done():
	case <-timeout(t).Done():
		t.Fatal("a connection outlived the relay")
	}
	b.logs.wait(t, "registered with relay", 2)
	c = dialRemote(t, rem)
	if _, err := c.Devices(timeout(t)); err != nil {
		t.Fatal(err)
	}
}

func TestCloseEndsRelayConnections(t *testing.T) {
	r := startRelay(t)
	b := startOnRelay(t, echoScript, r, true)
	b.logs.wait(t, "registered with relay", 1)
	_, rem := b.pairOverRelay(t, r)
	c := dialRemote(t, rem)
	if err := b.d.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-timeout(t).Done():
		t.Fatal("a relayed connection outlived Close")
	}
	// The control connection is gone too: the relay no longer knows the
	// host.
	if _, err := client.DialRemote(timeout(t), rem); err == nil {
		t.Fatal("reached a closed daemon through the relay")
	}
}

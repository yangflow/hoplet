package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/pair"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/testnet"
	"github.com/yangflow/hoplet/proto"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// hostB is a daemon with a direct listener behind a cuttable forwarder.
type hostB struct {
	d     *Daemon
	home  string
	fw    *testnet.Forwarder
	clock *clock
	local *client.Client
}

func startB(t *testing.T, s fake.Script, lim limits) *hostB {
	t.Helper()
	h, script := newHome(t, s)
	fw := testnet.NewForwarder(t)
	clk := &clock{t: time.Now()}
	cfg := testConfig(h, script)
	cfg.Listen, cfg.Routes, cfg.now, cfg.lim = "127.0.0.1:0", []string{fw.URL()}, clk.now, lim
	d := start(t, cfg)
	fw.SetTarget(d.NetworkAddr())
	return &hostB{d: d, home: h, fw: fw, clock: clk, local: dial(t, h, "cli@b")}
}

func newIdentity(t *testing.T) noise.Identity {
	t.Helper()
	id, err := noise.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// code opens a pairing on b and parses its code.
func (b *hostB) code(t *testing.T) pair.Code {
	t.Helper()
	pc, err := b.local.StartPairing(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := pair.Parse(pc.Code)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Expires.Equal(pc.ExpiresAt) {
		t.Fatalf("code expires %v, result says %v", c.Expires, pc.ExpiresAt)
	}
	return c
}

func remoteOf(c pair.Code, id noise.Identity) client.Remote {
	return client.Remote{Routes: c.Routes, HostKey: c.Key, Identity: id}
}

// pairWith pairs id with b and returns how to reach b.
func (b *hostB) pairWith(t *testing.T, id noise.Identity, name string) client.Remote {
	t.Helper()
	c := b.code(t)
	cl, err := client.Pair(timeout(t), remoteOf(c, id), c.PSK, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	return remoteOf(c, id)
}

func dialRemote(t *testing.T, r client.Remote) *client.Client {
	t.Helper()
	c, err := client.DialRemote(timeout(t), r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestNoListenerWithoutConfig(t *testing.T) {
	h, script := newHome(t, echoScript)
	d := start(t, testConfig(h, script))
	if a := d.NetworkAddr(); a != "" {
		t.Fatalf("listening on %s without network.listen", a)
	}
	c := dial(t, h, "cli")
	if _, err := c.StartPairing(timeout(t)); apiCode(err) != proto.CodeInvalid || !strings.Contains(err.Error(), "network.routes") {
		t.Fatalf("pairing without routes: %v", err)
	}
}

func TestPairing(t *testing.T) {
	b := startB(t, echoScript, limits{})
	a := newIdentity(t)
	code := b.code(t)
	if code.ID != b.d.srv.identity.ID() || code.Name == "" || code.Routes[0] != b.fw.URL() {
		t.Fatalf("code %+v", code)
	}
	cl, err := client.Pair(timeout(t), remoteOf(code, a), code.PSK, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if cl.Hello().HostID != string(code.ID) {
		t.Fatalf("hello host id %q", cl.Hello().HostID)
	}
	cl.Close()
	ds, err := b.local.Devices(timeout(t))
	if err != nil || len(ds) != 1 || ds[0].ID != string(a.ID()) || ds[0].Name != "laptop" || ds[0].RevokedAt != nil {
		t.Fatalf("devices %+v %v", ds, err)
	}
	stored, _ := b.d.store.Device(timeout(t), string(a.ID()))
	if !bytes.Equal(stored.PublicKey, a.Public()) {
		t.Fatal("stored key differs")
	}

	refused := func(what string, c pair.Code, psk []byte) {
		t.Helper()
		if _, err := client.Pair(timeout(t), remoteOf(c, newIdentity(t)), psk, "x"); !errors.Is(err, noise.ErrHandshake) {
			t.Fatalf("%s: %v, want a refused handshake", what, err)
		}
	}
	refused("used code", code, code.PSK)

	old := b.code(t)
	fresh := b.code(t)
	refused("replaced code", old, old.PSK)
	refused("wrong secret", fresh, bytes.Repeat([]byte{9}, noise.PSKSize))
	if _, err := client.Pair(timeout(t), remoteOf(fresh, newIdentity(t)), fresh.PSK, "y"); err != nil {
		t.Fatalf("a wrong secret voided the code: %v", err)
	}

	late := b.code(t)
	b.clock.add(pairingTTL)
	refused("expired code", late, late.PSK)

	// Pairing a revoked device again renames it and lifts the revocation.
	if err := b.local.RevokeDevice(timeout(t), string(a.ID())); err != nil {
		t.Fatal(err)
	}
	again := b.code(t)
	cl, err = client.Pair(timeout(t), remoteOf(again, a), again.PSK, "laptop 2")
	if err != nil {
		t.Fatal(err)
	}
	cl.Close()
	d, _ := b.d.store.Device(timeout(t), string(a.ID()))
	if d.Name != "laptop 2" || d.RevokedAt != nil {
		t.Fatalf("re-paired %+v", d)
	}
}

func TestDailyConnection(t *testing.T) {
	b := startB(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn}}, limits{})
	a := newIdentity(t)
	code := b.code(t)
	if _, err := client.DialRemote(timeout(t), remoteOf(code, a)); !errors.Is(err, noise.ErrHandshake) {
		t.Fatalf("unpaired device: %v", err)
	}
	r := b.pairWith(t, a, "laptop")
	wrong := r
	wrong.HostKey = newIdentity(t).Public()
	if _, err := client.DialRemote(timeout(t), wrong); err == nil {
		t.Fatal("dialed a host with the wrong key")
	}

	c := dialRemote(t, r)
	if c.Hello().HostID != string(code.ID) {
		t.Fatalf("hello %+v", c.Hello())
	}
	for _, call := range []func() error{
		func() error { _, err := c.StartPairing(timeout(t)); return err },
		func() error { _, err := c.Hosts(timeout(t)); return err },
		func() error { return c.AddHost(timeout(t), proto.Host{}) },
		func() error { return c.RemoveHost(timeout(t), "x") },
	} {
		if err := call(); apiCode(err) != proto.CodeUnauthorized {
			t.Fatalf("local-only method over Noise: %v", err)
		}
	}
	if ds, err := c.Devices(timeout(t)); err != nil || len(ds) != 1 {
		t.Fatalf("devices over Noise: %+v %v", ds, err)
	}

	// Approvals over Noise are decided by the device's host ID.
	sess := create(t, c, b.home)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "go", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	if err := c.Respond(timeout(t), proto.RespondParams{Session: sess.ID, Approval: "a1", Option: "once", Key: "r1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalResolved) == 1 })
	for _, ev := range w.events {
		if ev.Type == proto.TypeApprovalResolved {
			p, _ := ev.Decode()
			if by := p.(proto.ApprovalResolved).DecidedBy; by != string(a.ID()) {
				t.Fatalf("decided by %q, want %s", by, a.ID())
			}
		}
	}
}

func TestRoutesInOrder(t *testing.T) {
	b := startB(t, echoScript, limits{})
	r := b.pairWith(t, newIdentity(t), "laptop")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "ws://" + ln.Addr().String()
	ln.Close()
	r.Routes = []string{dead, b.fw.URL()}
	dialRemote(t, r)

	r.Routes = []string{dead, dead + "1"}
	_, err = client.DialRemote(timeout(t), r)
	if err == nil || strings.Count(err.Error(), dead) != 2 {
		t.Fatalf("every route's failure should be named: %v", err)
	}
}

func TestRevokeEndsConnections(t *testing.T) {
	b := startB(t, echoScript, limits{})
	a := newIdentity(t)
	r := b.pairWith(t, a, "laptop")
	c := dialRemote(t, r)
	if err := b.local.RevokeDevice(timeout(t), string(a.ID())[:minDevicePrefix]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-timeout(t).Done():
		t.Fatal("the revoked device stayed connected")
	}
	if _, err := client.DialRemote(timeout(t), r); !errors.Is(err, noise.ErrHandshake) {
		t.Fatalf("revoked device reconnected: %v", err)
	}
	if ds, _ := b.local.Devices(timeout(t)); len(ds) != 1 || ds[0].RevokedAt == nil {
		t.Fatalf("devices %+v", ds)
	}
}

func TestRevokeNeedsOneDevice(t *testing.T) {
	b := startB(t, echoScript, limits{})
	ctx := timeout(t)
	for _, id := range []string{"abcdefgh", "abcdxyz1"} {
		if err := b.d.store.PairDevice(ctx, store.Device{ID: id, PublicKey: []byte{1}, Name: id, PairedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]proto.ErrorCode{"abc": proto.CodeInvalid, "abcd": proto.CodeInvalid, "zzzz": proto.CodeNotFound} {
		if err := b.local.RevokeDevice(ctx, id); apiCode(err) != want {
			t.Errorf("revoke %q: %v, want %s", id, err, want)
		}
	}
	if err := b.local.RevokeDevice(ctx, "abcd"); !strings.Contains(err.Error(), "abcdefgh, abcdxyz1") {
		t.Errorf("ambiguous prefix should list the devices: %v", err)
	}
	if err := b.local.RevokeDevice(ctx, "abcde"); err != nil {
		t.Fatal(err)
	}
}

func TestHosts(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	c := dial(t, h, "cli")
	id := newIdentity(t)
	host := proto.Host{ID: string(id.ID()), Key: id.Public(), Name: "b", Routes: []string{"ws://192.0.2.1:7443"}}
	if err := c.AddHost(timeout(t), host); err != nil {
		t.Fatal(err)
	}
	other := newIdentity(t)
	for name, h := range map[string]proto.Host{
		"taken name": {ID: string(other.ID()), Key: other.Public(), Name: "b", Routes: host.Routes},
		"wrong id":   {ID: string(id.ID()), Key: other.Public(), Name: "c", Routes: host.Routes},
		"no routes":  {ID: string(other.ID()), Key: other.Public(), Name: "c"},
	} {
		want := proto.CodeInvalid
		if name == "taken name" {
			want = proto.CodeConflict
		}
		if err := c.AddHost(timeout(t), h); apiCode(err) != want {
			t.Errorf("%s: %v, want %s", name, err, want)
		}
	}
	hs, err := c.Hosts(timeout(t))
	if err != nil || len(hs) != 1 || hs[0].Name != "b" || !bytes.Equal(hs[0].Key, id.Public()) || hs[0].AddedAt.IsZero() {
		t.Fatalf("hosts %+v %v", hs, err)
	}
	if err := c.RemoveHost(timeout(t), string(id.ID())); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveHost(timeout(t), "b"); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("remove again: %v", err)
	}
}

func TestNoiseListenerHTTP(t *testing.T) {
	b := startB(t, echoScript, limits{hello: 200 * time.Millisecond})
	base := "http://" + b.d.NetworkAddr()
	for path, want := range map[string]int{"/v1": http.StatusNotFound, "/": http.StatusNotFound} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	_, resp, err := websocket.Dial(timeout(t), "ws://"+b.d.NetworkAddr()+proto.NoisePath,
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://example.com"}}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("browser request: %v %v", resp, err)
	}
	resp.Body.Close()

	// A connection that never completes the handshake is closed.
	ws, _, err := websocket.Dial(timeout(t), "ws://"+b.d.NetworkAddr()+proto.NoisePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	if _, _, err := ws.Read(timeout(t)); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a silent connection stayed open: %v", err)
	}
}

// TestRemoteHeartbeat checks that a device that stops answering pings is
// dropped.
func TestRemoteHeartbeat(t *testing.T) {
	b := startB(t, echoScript, limits{ping: 50 * time.Millisecond, pingTimeout: 100 * time.Millisecond})
	a := newIdentity(t)
	r := b.pairWith(t, a, "laptop")
	ws, _, err := websocket.Dial(timeout(t), r.Routes[0]+proto.NoisePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := noise.Dial(timeout(t), noise.NewWSConn(ws), noise.DialConfig{Identity: a, Kind: noise.KindDaily, PeerStatic: r.HostKey})
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	hello, _ := json.Marshal(proto.ClientFrame{ID: 1, Method: proto.MethodHello, Params: json.RawMessage(`{"protocol_major":1}`)})
	if err := ch.WriteFrame(timeout(t), hello); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.ReadFrame(timeout(t)); err != nil {
		t.Fatal(err)
	}
	// Not reading means pings go unanswered.
	waitCond(t, "the silent device to be dropped", func() bool { return b.d.srv.active.Load() == 1 })
}

// TestRemoteCatchUp cuts a remote subscriber mid-session; after redialing
// it resumes from its cursor with nothing missing or repeated.
func TestRemoteCatchUp(t *testing.T) {
	turn := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	b := startB(t, fake.Script{Session: "n1", Turns: []fake.Turn{turn, turn}}, limits{})
	r := b.pairWith(t, newIdentity(t), "laptop")
	c := dialRemote(t, r)
	sess := create(t, c, b.home)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "one", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(1))
	seen := w.events
	b.fw.Cut()
	select {
	case <-c.Done():
	case <-timeout(t).Done():
		t.Fatal("the cut connection stayed up")
	}
	if _, err := b.local.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "two", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	c2 := dialRemote(t, r)
	w2 := watch(t, c2, sess.ID, seen[len(seen)-1].Seq)
	w2.until(turnEnded(1))
	all := append(append([]proto.Event(nil), seen...), w2.events...)
	for i, ev := range all {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d:%s", i, ev.Seq, describe(all))
		}
	}
	if got := w2.texts(2); len(got) == 0 || got[len(got)-1] != "two" {
		t.Fatalf("turn 2 texts %v", got)
	}
}

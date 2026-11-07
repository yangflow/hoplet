package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/pair"
	"github.com/yangflow/hoplet/internal/relay"
	"github.com/yangflow/hoplet/internal/testnet"
	"github.com/yangflow/hoplet/proto"
)

// relayLogs records a relay's log messages for a test to wait on.
type relayLogs struct {
	mu      sync.Mutex
	msgs    []string
	changed chan struct{}
}

func (l *relayLogs) Enabled(context.Context, slog.Level) bool { return true }
func (l *relayLogs) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *relayLogs) WithGroup(string) slog.Handler            { return l }

func (l *relayLogs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, r.Message)
	close(l.changed)
	l.changed = make(chan struct{})
	return nil
}

// wait returns once msg has been logged n times.
func (l *relayLogs) wait(t *testing.T, msg string, n int) {
	t.Helper()
	timeout := time.After(30 * time.Second)
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
			t.Fatalf("waited for relay log %q ×%d", msg, n)
		}
	}
}

// e2eRelay is a relay in the test process on a loopback port, with a test
// certificate the test's processes trust through HOPLET_TEST_RELAY_CA.
type e2eRelay struct {
	addr, dir string
	conf      *tls.Config
	logs      *relayLogs
	cancel    context.CancelFunc
	done      chan error
}

func startE2ERelay(t *testing.T) *e2eRelay {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	conf := ts.TLS.Clone()
	ca := filepath.Join(t.TempDir(), "relay.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	ts.Close()
	t.Setenv("HOPLET_TEST_RELAY_CA", ca)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &e2eRelay{addr: ln.Addr().String(), dir: t.TempDir(), conf: conf, logs: &relayLogs{changed: make(chan struct{})}}
	r.serve(ln)
	t.Cleanup(func() { r.stop(t) })
	return r
}

func (r *e2eRelay) url() string { return "wss://" + r.addr }

func (r *e2eRelay) serve(ln net.Listener) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan error, 1)
	s := &relay.Server{Dir: r.dir, Logger: slog.New(r.logs)}
	go func() { r.done <- s.Serve(ctx, ln, r.conf) }()
}

func (r *e2eRelay) stop(t *testing.T) {
	if r.cancel == nil {
		return
	}
	r.cancel()
	if err := <-r.done; err != nil {
		t.Error(err)
	}
	r.cancel = nil
}

// restart ends every connection through the relay and serves again on the
// same address.
func (r *e2eRelay) restart(t *testing.T) {
	t.Helper()
	r.stop(t)
	ln, err := net.Listen("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	r.serve(ln)
}

// TestReachAHostThroughARelay runs two homes whose only working route is
// a relay while B's direct route is down: A pairs with B and drives a
// session through the relay, a watch outlives a relay restart, and once
// the direct route works again the watch moves to it, printing every
// event once.
func TestReachAHostThroughARelay(t *testing.T) {
	a, _ := testHome(t)
	b := shortHome(t)
	onA := func() { t.Setenv("HOPLET_HOME", a) }
	onB := func() { t.Setenv("HOPLET_HOME", b) }
	r := startE2ERelay(t)
	t.Setenv("HOPLET_TEST_PROBE_EVERY", "100ms")

	fakeBin, err := fake.BuildBinary(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	turn := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	script, err := fake.WriteScript(b, fake.Script{Session: "n1", Turns: []fake.Turn{turn, turn, turn}})
	if err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	bID, err := noise.EnsureIdentity(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Allow(r.dir, bID.ID()); err != nil {
		t.Fatal(err)
	}
	// B's direct route leads nowhere until the test points it at B.
	fw := testnet.NewForwarder(t)
	fw.SetTarget(freeAddr(t))
	listen := freeAddr(t)
	cfg := fmt.Sprintf("[network]\nlisten = %q\nroutes = [%q]\nrelays = [%q]\n", listen, fw.URL(), r.url())
	if err := os.WriteFile(filepath.Join(b, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	startDaemon(t, b, nil, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script, "HOPLET_TEST_PROBE="+probe.URL)
	startDaemonProcess(t, a, "HOPLET_TEST_PROBE="+probe.URL)
	localB := localClient(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r.logs.wait(t, "host registered", 1)

	// The code lists the direct route first, then the relay.
	onB()
	codeText, _, _ := strings.Cut(mustCLI(t, "pair"), "\n")
	code, err := pair.Parse(codeText)
	if err != nil {
		t.Fatal(err)
	}
	relayRoute := r.url() + proto.RelayConnectPath
	if len(code.Routes) != 2 || code.Routes[0] != fw.URL() || code.Routes[1] != relayRoute {
		t.Fatalf("code routes %v", code.Routes)
	}

	onA()
	mustCLI(t, "host", "add", codeText, "--name", "b")
	work := filepath.Join(b, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(mustCLI(t, "--host", "b", "session", "new", "--harness", "fake", "--dir", work, "first"))
	mustCLI(t, "--host", "b", "session", "wait", id, "--for", "idle", "--timeout", "15s")

	// The session came through the relay, so B is registered: doctor says
	// so, along with the listener and B's identity.
	onB()
	_, out, _ := cli(t, "doctor", "--json")
	var rep proto.DoctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out)
	}
	checks := map[string]proto.Check{}
	for _, c := range rep.Checks {
		checks[c.Name] = c
	}
	for name, has := range map[string]string{
		"identity":         "host ID " + string(bID.ID()),
		"listener":         "listening on " + listen + "; routes " + fw.URL(),
		"relay " + r.url(): "registered since",
	} {
		if c := checks[name]; c.Status != proto.CheckOK || !strings.Contains(c.Detail, has) {
			t.Errorf("doctor %s: %+v", name, c)
		}
	}
	onA()

	// The watch is on the relay; a relay restart ends its connection and
	// it continues once B has registered again.
	w := startWatch(t, "--host", "b", "session", "watch", id, "--json")
	w.until(t, lastSeq(ctx, t, localB, id))
	r.restart(t)
	r.logs.wait(t, "host registered", 2)
	w.stderr.wait(t, "reconnected")
	if _, err := localB.Send(ctx, proto.SendParams{Session: proto.SessionID(id), Text: "second", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	onB()
	mustCLI(t, "session", "wait", id, "--for", "idle", "--timeout", "15s")
	w.until(t, lastSeq(ctx, t, localB, id))

	// The direct route works again: the watch moves to it.
	fw.SetTarget(listen)
	w.stderr.wait(t, "switched to "+fw.URL())
	if _, err := localB.Send(ctx, proto.SendParams{Session: proto.SessionID(id), Text: "third", Key: "k3"}); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, "session", "wait", id, "--for", "idle", "--timeout", "15s")
	w.until(t, lastSeq(ctx, t, localB, id))
	stderr := w.stop(t)
	for i, ev := range w.events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("watch event %d has seq %d: events missing or repeated", i, ev.Seq)
		}
	}
	// Failed probes print nothing: every message is about the connection.
	// (A reconnect error spans lines, one per route.)
	for line := range strings.Lines(stderr) {
		if !strings.HasPrefix(line, "hoplet ") {
			continue
		}
		ok := false
		for _, p := range []string{"disconnected: ", "reconnecting: ", "reconnected", "switched to " + fw.URL()} {
			ok = ok || strings.HasPrefix(line, "hoplet session watch: "+p)
		}
		if !ok {
			t.Errorf("watch stderr line %q", line)
		}
	}
	if n := strings.Count(stderr, "switched to"); n != 1 {
		t.Errorf("switched %d times:\n%s", n, stderr)
	}
}

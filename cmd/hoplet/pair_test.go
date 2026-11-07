package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/pair"
	"github.com/yangflow/hoplet/internal/testnet"
	"github.com/yangflow/hoplet/proto"
)

func TestHostFlagPlacement(t *testing.T) {
	testHome(t)
	for _, args := range [][]string{
		{"--host"},
		{"--host", "b"},
		{"--host=", "session", "list"},
		{"--host", "b", "daemon", "status"},
		{"--host", "b", "pair"},
		{"--host=b", "host", "list"},
		{"--host", "b", "version"},
	} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("hoplet %s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
}

func TestHostAddChecksTheCodeFirst(t *testing.T) {
	testHome(t)
	id, err := noise.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	// No daemon runs: a check that needed the network or the daemon would
	// fail differently.
	code := pair.Code{
		ID: id.ID(), Key: id.Public(), Name: "b", Routes: []string{"ws://127.0.0.1:1"},
		PSK: bytes.Repeat([]byte{1}, noise.PSKSize), Expires: time.Now().Add(-time.Minute),
	}
	if c, _, errs := cli(t, "host", "add", code.String()); c != 1 || !strings.Contains(errs, "expired") {
		t.Fatalf("expired code: exit %d, %q", c, errs)
	}
	if c, _, errs := cli(t, "host", "add", "hoplet1:nonsense"); c != 1 || !strings.Contains(errs, "bad pairing code") {
		t.Fatalf("bad code: exit %d, %q", c, errs)
	}
}

// freeAddr returns a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// shortHome is a short temporary directory for a second Hoplet home.
func shortHome(t *testing.T) string {
	t.Helper()
	h, err := os.MkdirTemp("", "hb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(h) })
	return h
}

// localClient dials the daemon of home h.
func localClient(t *testing.T, h string) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := dialHome(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestPairAndReachAHost runs two homes, each with its own daemon: B prints
// a pairing code, A adds B with it, then drives a session on B through a
// forwarder that the test cuts, and B revokes A.
func TestPairAndReachAHost(t *testing.T) {
	a, _ := testHome(t)
	b := shortHome(t)
	onA := func() { t.Setenv("HOPLET_HOME", a) }
	onB := func() { t.Setenv("HOPLET_HOME", b) }

	fakeBin, err := fake.BuildBinary(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	turn := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	script, err := fake.WriteScript(b, fake.Script{Session: "n1", Turns: []fake.Turn{turn, turn}})
	if err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	fw := testnet.NewForwarder(t)
	listen := freeAddr(t)
	fw.SetTarget(listen)
	cfg := fmt.Sprintf("[network]\nlisten = %q\nroutes = [%q]\n", listen, fw.URL())
	if err := os.WriteFile(filepath.Join(b, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	startDaemon(t, b, nil, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script, "HOPLET_TEST_PROBE="+probe.URL)
	startDaemonProcess(t, a, "HOPLET_TEST_PROBE="+probe.URL)
	localA, localB := localClient(t, a), localClient(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// B prints a code: the daemon put network.routes in it.
	onB()
	out := mustCLI(t, "pair")
	codeText, _, _ := strings.Cut(out, "\n")
	code, err := pair.Parse(codeText)
	if err != nil {
		t.Fatalf("pair printed %q: %v", out, err)
	}
	if len(code.Routes) != 1 || code.Routes[0] != fw.URL() {
		t.Fatalf("code routes %v, want [%s]", code.Routes, fw.URL())
	}

	// A name taken by another host is refused before the code is used.
	onA()
	other, err := noise.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	taken := proto.Host{ID: string(other.ID()), Key: other.Public(), Name: "taken", Routes: []string{"ws://127.0.0.1:1"}}
	if err := localA.AddHost(ctx, taken); err != nil {
		t.Fatal(err)
	}
	if c, _, errs := cli(t, "host", "add", codeText, "--name", "taken"); c != 1 || !strings.Contains(errs, "exists already") {
		t.Fatalf("host add with a taken name: exit %d, %q", c, errs)
	}
	if out := mustCLI(t, "host", "add", codeText, "--name", "b"); !strings.Contains(out, "Added host b ("+string(code.ID)+")") {
		t.Fatalf("host add: %q", out)
	}
	if c, _, _ := cli(t, "host", "add", codeText, "--name", "b2"); c != 1 {
		t.Fatalf("host add with a used code: exit %d, want 1", c)
	}
	if out := mustCLI(t, "host", "list"); !strings.Contains(out, "b ") || !strings.Contains(out, string(code.ID)) {
		t.Fatalf("host list: %q", out)
	}
	if c, _, errs := cli(t, "--host", "nope", "session", "list"); c != 1 || !strings.Contains(errs, `no host is named "nope"`) {
		t.Fatalf("--host nope: exit %d, %q", c, errs)
	}

	// A drives a session on B; B records A's device as the approver.
	work := filepath.Join(b, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(mustCLI(t, "--host", "b", "session", "new", "--harness", "fake", "--dir", work, "first"))
	mustCLI(t, "--host", "b", "session", "wait", id, "--for", "idle", "--timeout", "15s")
	var devs []proto.Device
	if err := json.Unmarshal([]byte(mustCLI(t, "--host", string(code.ID), "device", "list", "--json")), &devs); err != nil {
		t.Fatal(err)
	}
	aID, err := noise.LoadIdentity(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].ID != string(aID.ID()) {
		t.Fatalf("devices on B: %+v, want A (%s)", devs, aID.ID())
	}

	// session watch outlives a cut: it reconnects and continues after the
	// last event it printed.
	w := startWatch(t, "--host", "b", "session", "watch", id, "--json")
	w.until(t, lastSeq(ctx, t, localB, id))
	fw.Cut()
	if _, err := localB.Send(ctx, proto.SendParams{Session: proto.SessionID(id), Text: "second", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	onB()
	mustCLI(t, "session", "wait", id, "--for", "idle", "--timeout", "15s")
	w.until(t, lastSeq(ctx, t, localB, id))
	stderr := w.stop(t)
	if !strings.Contains(stderr, "disconnected") || !strings.Contains(stderr, "reconnected") {
		t.Fatalf("watch stderr: %q", stderr)
	}
	for i, ev := range w.events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("watch event %d has seq %d: events missing or repeated", i, ev.Seq)
		}
	}

	// A prefix two devices share names neither.
	onB()
	x, y := twins(t)
	for _, idn := range []noise.Identity{x, y} {
		pc, err := localB.StartPairing(ctx)
		if err != nil {
			t.Fatal(err)
		}
		c, err := pair.Parse(pc.Code)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := client.Pair(ctx, client.Remote{Routes: c.Routes, HostKey: c.Key, Identity: idn}, c.PSK, "twin")
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	prefix := string(x.ID())[:4]
	if c, _, errs := cli(t, "device", "revoke", prefix); c != 1 || !strings.Contains(errs, string(x.ID())) || !strings.Contains(errs, string(y.ID())) {
		t.Fatalf("revoke %s: exit %d, %q", prefix, c, errs)
	}

	// B revokes A by a prefix: A's connections end and new ones fail.
	held, err := dialHost(ctx, a, "b")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	mustCLI(t, "device", "revoke", string(aID.ID())[:6])
	select {
	case <-held.Done():
	case <-ctx.Done():
		t.Fatal("A's connection outlived the revocation")
	}
	onA()
	if c, _, _ := cli(t, "--host", "b", "session", "list"); c != 1 {
		t.Fatalf("session list after the revocation: exit %d, want 1", c)
	}
}

// twins returns two identities whose IDs share their first four
// characters.
func twins(t *testing.T) (noise.Identity, noise.Identity) {
	t.Helper()
	seen := map[string]noise.Identity{}
	for {
		id, err := noise.NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		p := string(id.ID())[:4]
		if other, ok := seen[p]; ok {
			return other, id
		}
		seen[p] = id
	}
}

// lastSeq is the session's last event sequence number on the host c
// reaches.
func lastSeq(ctx context.Context, t *testing.T, c *client.Client, id string) uint64 {
	t.Helper()
	st, err := c.Session(ctx, proto.SessionID(id))
	if err != nil {
		t.Fatal(err)
	}
	return st.Session.LastSeq
}

// watch is a `session watch --json` process and the events it printed.
type watch struct {
	cmd    *exec.Cmd
	lines  *bufio.Scanner
	stderr *syncBuffer
	events []proto.Event
}

func startWatch(t *testing.T, args ...string) *watch {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "HOPLET_TEST_MAIN=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	w := &watch{cmd: cmd, stderr: &syncBuffer{}}
	cmd.Stderr = w.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	w.lines = bufio.NewScanner(stdout)
	w.lines.Buffer(nil, 1<<20)
	return w
}

// until reads events up to seq.
func (w *watch) until(t *testing.T, seq uint64) {
	t.Helper()
	for len(w.events) == 0 || w.events[len(w.events)-1].Seq < seq {
		if !w.lines.Scan() {
			t.Fatalf("watch ended before seq %d: %v\n%s", seq, w.lines.Err(), w.stderr)
		}
		var ev proto.Event
		if err := json.Unmarshal(w.lines.Bytes(), &ev); err != nil {
			t.Fatalf("watch line %q: %v", w.lines.Text(), err)
		}
		w.events = append(w.events, ev)
	}
}

// stop interrupts the watch and returns what it wrote to stderr.
func (w *watch) stop(t *testing.T) string {
	t.Helper()
	if err := w.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := w.cmd.Wait(); err != nil {
		t.Fatalf("watch after SIGINT: %v\n%s", err, w.stderr)
	}
	return w.stderr.String()
}

// syncBuffer is a bytes.Buffer a process writes while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

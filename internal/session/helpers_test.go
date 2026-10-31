package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

var hopletExe, fakeExe string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sessbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hopletExe = filepath.Join(dir, "hoplet")
	if out, err := exec.Command("go", "build", "-o", hopletExe, "github.com/yangflow/hoplet/cmd/hoplet").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build hoplet: %v\n%s", err, out)
		os.Exit(1)
	}
	if fakeExe, err = fake.BuildBinary(context.Background(), dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is a deterministic test environment for harnesses.
type env struct {
	t      *testing.T
	root   string // holds hoplet.db and sessions/
	work   string
	script string
	store  *store.Store
}

func newEnv(t *testing.T, s fake.Script) *env {
	t.Helper()
	root, err := os.MkdirTemp("", "hs") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	// Detached shims outlive their manager by design; stop every shim under
	// this root (SIGTERM terminates its harness too) so tests leak nothing.
	t.Cleanup(func() { stopShims(t, root) })
	script, err := fake.WriteScript(root, s)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, root: root, work: work, script: script, store: st}
}

// manager returns a new Manager over the env's store, as a fresh daemon
// would create one. The caller closes it.
func (e *env) manager() *Manager {
	e.t.Helper()
	m, err := New(Config{
		Store: e.store, Root: filepath.Join(e.root, "s"), Spawner: ExecSpawner{Exe: hopletExe},
		Harnesses: map[string]harness.StdioHarness{"fake": &fake.Harness{Binary: fakeExe, Script: e.script}},
		Env:       []string{"PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) create(m *Manager) proto.Session {
	e.t.Helper()
	sess, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work})
	if err != nil {
		e.t.Fatal(err)
	}
	return sess
}

// waitFor polls the store until cond holds; the store, not the harness, is
// what the test observes.
func (e *env) waitFor(id proto.SessionID, what string, cond func([]proto.Event) bool) []proto.Event {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		evs, err := e.store.Events(context.Background(), id, 0, 10000)
		if err != nil {
			e.t.Fatal(err)
		}
		if cond(evs) {
			return evs
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s; events: %s", what, describe(evs))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func payloads(t *testing.T, evs []proto.Event) []proto.Payload {
	t.Helper()
	out := make([]proto.Payload, len(evs))
	for i, ev := range evs {
		p, err := ev.Decode()
		if err != nil {
			t.Fatal(err)
		}
		out[i] = p
	}
	return out
}

func describe(evs []proto.Event) string {
	s := ""
	for _, ev := range evs {
		s += fmt.Sprintf("\n  %d t%d %s %s", ev.Seq, ev.Turn, ev.Type, ev.Payload)
	}
	return s
}

func turnEnded(evs []proto.Event) bool {
	for _, ev := range evs {
		if ev.Type == proto.TypeTurnEnded {
			return true
		}
	}
	return false
}

func count(evs []proto.Event, typ proto.EventType) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// waitShimGone waits until no shim process owns the session's directory.
func waitShimGone(t *testing.T, m *Manager, id proto.SessionID) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := exec.Command("pgrep", "-f", "shim --dir "+m.dir(id)).Run(); err != nil {
			return // pgrep exits non-zero when nothing matches
		}
		if time.Now().After(deadline) {
			t.Fatal("shim still running")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stopShims terminates every shim whose directory is under root and waits
// for them to exit.
func stopShims(t *testing.T, root string) {
	t.Helper()
	pattern := "shim --dir " + root
	_ = exec.Command("pkill", "-TERM", "-f", pattern).Run() // exits 1 when nothing matches
	deadline := time.Now().Add(15 * time.Second)
	for exec.Command("pgrep", "-f", pattern).Run() == nil {
		if time.Now().After(deadline) {
			_ = exec.Command("pkill", "-KILL", "-f", pattern).Run()
			t.Error("shims did not exit after SIGTERM")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

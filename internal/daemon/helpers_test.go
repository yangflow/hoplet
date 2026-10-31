package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

var hopletExe, fakeExe string

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		os.Exit(runHelper())
	}
	dir, err := os.MkdirTemp("", "dbin")
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

// newHome returns a short home directory (Unix socket paths are limited)
// holding the script; every shim under it is stopped when the test ends.
func newHome(t *testing.T, s fake.Script) (dir, script string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Cleanup(func() { stopShims(t, dir) })
	if script, err = fake.WriteScript(dir, s); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, script
}

func fakeHarnesses(script string) map[string]harness.Harness {
	return map[string]harness.Harness{"fake": &fake.Harness{Binary: fakeExe, Script: script}}
}

func testConfig(home, script string) Config {
	return Config{
		Home: home, Version: "test", Exe: hopletExe, Harnesses: fakeHarnesses(script),
		Env: env.Static{"PATH=" + os.Getenv("PATH")},
	}
}

// start starts an in-process daemon; it is closed when the test ends.
func start(t *testing.T, cfg Config) *Daemon {
	t.Helper()
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}

// dial connects to the home's daemon as device; the client is closed when
// the test ends.
func dial(t *testing.T, home, device string) *client.Client {
	t.Helper()
	o, err := client.HomeOptions(home, device)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.Dial(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func timeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func create(t *testing.T, c *client.Client, home string) proto.Session {
	t.Helper()
	sess, err := c.Create(timeout(t), proto.CreateParams{Mode: proto.ModeCode, Harness: "fake", Directory: filepath.Join(home, "work")})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// watcher follows a session subscription, keeping what it has seen.
type watcher struct {
	t      *testing.T
	sub    *client.Subscription
	events []proto.Event
	queue  []proto.QueuedMessage
	snap   *proto.SessionSnapshot
}

func watch(t *testing.T, c *client.Client, id proto.SessionID, after uint64) *watcher {
	t.Helper()
	sub, err := c.SubscribeSession(timeout(t), proto.SessionParams{Session: id, AfterSeq: after})
	if err != nil {
		t.Fatal(err)
	}
	return &watcher{t: t, sub: sub}
}

// next reads one item.
func (w *watcher) next() proto.SessionItem {
	w.t.Helper()
	var it proto.SessionItem
	if err := w.sub.Next(timeout(w.t), &it); err != nil {
		w.t.Fatalf("next item: %v; seen %s", err, describe(w.events))
	}
	if it.Snapshot != nil {
		w.snap = it.Snapshot
		w.events = append([]proto.Event(nil), it.Snapshot.Events...)
	}
	w.events = append(w.events, it.Events...)
	if it.Queue != nil {
		w.queue = it.Queue.Messages
	}
	return it
}

// until reads items until cond holds over everything seen.
func (w *watcher) until(cond func(*watcher) bool) {
	w.t.Helper()
	for !cond(w) {
		w.next()
	}
}

func turnEnded(n int) func(*watcher) bool {
	return func(w *watcher) bool { return w.count(proto.TypeTurnEnded) >= n }
}

func (w *watcher) count(typ proto.EventType) int {
	n := 0
	for _, ev := range w.events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// texts returns the text of every message in turn n.
func (w *watcher) texts(n uint32) []string {
	w.t.Helper()
	var out []string
	for _, ev := range w.events {
		if ev.Type != proto.TypeMessage || ev.Turn != n {
			continue
		}
		p, err := ev.Decode()
		if err != nil {
			w.t.Fatal(err)
		}
		out = append(out, p.(proto.Message).Text)
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

func apiCode(err error) proto.ErrorCode {
	var ae *proto.APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
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

// waitCond polls cond until it holds.
func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

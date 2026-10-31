package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/proto"
)

var echoTurn = fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}

// TestQueuedMessageAfterAnInstantTurnIsSent covers a turn that starts and
// ends in one ingestion batch while the next message waits in the queue:
// the store's change arrives before the manager clears its pending flag, so
// the drainer must also be woken by the manager. The interleaving is not
// forced (the manager's test hooks are internal to its package), so this
// exercises the path probabilistically.
func TestQueuedMessageAfterAnInstantTurnIsSent(t *testing.T) {
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{echoTurn, echoTurn}})
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	sess := create(t, c, h)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "one", Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "two", Key: "m2"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(2))
	if got := w.texts(2); len(got) != 2 || got[0] != "two" {
		t.Fatalf("turn 2 messages %v", got)
	}
}

// spoolStarts counts the harness starts recorded in the session's spool.
func spoolStarts(t *testing.T, h string, id proto.SessionID) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home.Sessions(h), string(id), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), `"k":"start"`)
}

func TestStopHoldsTheQueue(t *testing.T) {
	// A restarted harness resumes at the echo turn.
	h, script := newHome(t, fake.Script{Session: "n1", ResumeTurn: 1, Turns: []fake.Turn{approvalTurn, echoTurn}})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	sess := create(t, c, h)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "first", Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	if res, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "second", Key: "m2"}); err != nil || !res.Queued {
		t.Fatalf("send while busy: %+v %v", res, err)
	}
	if err := c.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	q, err := c.Queue(ctx, sess.ID)
	if err != nil || len(q) != 1 || q[0].Key != "m2" || q[0].Error != "the session was stopped" {
		t.Fatalf("queue after stop %+v %v", q, err)
	}
	dir := filepath.Join(home.Sessions(h), string(sess.ID))
	waitCond(t, "the shim to exit", func() bool {
		return exec.Command("pgrep", "-f", "shim --dir "+dir).Run() != nil
	})
	if n := spoolStarts(t, h, sess.ID); n != 1 {
		t.Fatalf("%d harness starts after the stop, want 1", n)
	}
	if last, open, err := d.store.TurnState(ctx, sess.ID); err != nil || last != 1 || open {
		t.Fatalf("turn state after the stop: last %d open %v %v", last, open, err)
	}

	// Editing the held message resumes the queue, which starts the harness.
	if err := c.EditQueued(ctx, proto.QueueEditParams{Session: sess.ID, Key: "m2", Text: "edited"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(2))
	if got := w.texts(2); len(got) != 2 || got[0] != "edited" {
		t.Fatalf("turn 2 messages %v", got)
	}
	if n := spoolStarts(t, h, sess.ID); n != 2 {
		t.Fatalf("%d harness starts after the edit, want 2", n)
	}
}

func TestEventsBeforePagesBack(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.lim = limits{page: 8}
	d := start(t, cfg)
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	sess := create(t, c, h)
	for n := uint32(1); n <= 4; n++ {
		appendTurn(t, d, sess.ID, n) // seqs 1..20
	}
	seqs := func(p proto.EventsPage) []uint64 {
		var out []uint64
		for _, ev := range p.Events {
			out = append(out, ev.Seq)
		}
		return out
	}
	for name, tc := range map[string]struct {
		before uint64
		limit  int
		want   []uint64
		more   bool
	}{
		"middle":         {11, 5, []uint64{6, 7, 8, 9, 10}, true},
		"start":          {3, 5, []uint64{1, 2}, false},
		"exact":          {6, 5, []uint64{1, 2, 3, 4, 5}, false},
		"nothing older":  {1, 5, nil, false},
		"default limit":  {21, 0, []uint64{13, 14, 15, 16, 17, 18, 19, 20}, true},
		"limit too high": {21, 100, []uint64{13, 14, 15, 16, 17, 18, 19, 20}, true},
	} {
		p, err := c.EventsBefore(ctx, proto.EventsParams{Session: sess.ID, BeforeSeq: tc.before, Limit: tc.limit})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := seqs(p); !equalSeqs(got, tc.want) || p.More != tc.more || p.Events == nil {
			t.Errorf("%s: seqs %v more %v, want %v more %v", name, got, p.More, tc.want, tc.more)
		}
	}
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUnansweredHeartbeatClosesTheConnection(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.lim = limits{ping: 100 * time.Millisecond, pingTimeout: 100 * time.Millisecond}
	d := start(t, cfg)
	rawHello(t, h) // never reads again, so it never answers a ping
	waitCond(t, "the silent connection to close", func() bool { return d.srv.active.Load() == 0 })
}

func TestTooLongSocketPathIsRefused(t *testing.T) {
	h, script := newHome(t, echoScript)
	long := filepath.Join(h, strings.Repeat("d", 100))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Start(context.Background(), testConfig(long, script))
	if err == nil || !strings.Contains(err.Error(), "too long for a Unix socket") {
		t.Fatalf("start with a long home: %v", err)
	}
}

package daemon

import (
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/proto"
)

// TestHandoffReportsProgress checks that a handed-off session reports
// each approval it waits on and each turn's end to the session that
// started it, once each, and nothing once that session is archived.
func TestHandoffReportsProgress(t *testing.T) {
	echo := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn, {}, echo}})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	parent, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModePersonal, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := d.mgr.Create(ctx, session.CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: filepath.Join(h, "work"),
		Permission: proto.ModeConfirm, Parent: parent.ID, Owner: proto.OwnerMe})
	if err != nil {
		t.Fatal(err)
	}
	pw, cw := watch(t, c, parent.ID, 0), watch(t, c, code.ID, 0)
	if _, err := c.Send(ctx, proto.SendParams{Session: code.ID, Text: "fix it", Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	// The approval report starts the parent's turn, which waits on its
	// own approval; the next reports wait in its queue.
	pw.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	if err := c.Respond(ctx, proto.RespondParams{Session: code.ID, Approval: "a1", Option: "once", Key: "a"}); err != nil {
		t.Fatal(err)
	}
	cw.until(turnEnded(1))
	pw.until(func(w *watcher) bool { return len(w.queue) == 1 && w.queue[0].From == code.ID })
	if _, err := c.Send(ctx, proto.SendParams{Session: code.ID, Text: "more", Key: "m2"}); err != nil {
		t.Fatal(err)
	}
	cw.until(turnEnded(2))
	pw.until(func(w *watcher) bool { return len(w.queue) == 2 })
	if err := c.Respond(ctx, proto.RespondParams{Session: parent.ID, Approval: "a1", Option: "once", Key: "a"}); err != nil {
		t.Fatal(err)
	}
	pw.until(func(w *watcher) bool { return len(w.queue) == 0 })
	pw.until(turnEnded(3))
	texts := func() []string {
		var out []string
		for _, m := range reportsOf(t, d.store, parent.ID) {
			if m.From != code.ID {
				t.Fatalf("report from %s", m.From)
			}
			out = append(out, m.Text)
		}
		return out
	}
	want := []string{"Waiting for the user to approve: Run go test", "approved:once", noReply}
	if got := texts(); !slices.Equal(got, want) {
		t.Fatalf("reports %q, want %q", got, want)
	}

	// A reporter that has seen nothing, as after a restart, reports
	// nothing again.
	fresh := func() *handoffs {
		ho := &handoffs{store: d.store, log: slog.New(slog.DiscardHandler), seen: map[proto.SessionID]uint64{}}
		ho.check(ctx)
		return ho
	}
	fresh()
	if q, err := d.store.Queue(ctx, parent.ID); err != nil || len(q) != 0 {
		t.Fatalf("queue after a fresh check %+v %v", q, err)
	}
	if got := texts(); !slices.Equal(got, want) {
		t.Fatalf("reports after a fresh check %q", got)
	}

	// An archived parent gets nothing, and the turn counts as reported.
	if err := c.Archive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, proto.SendParams{Session: code.ID, Text: "last", Key: "m3"}); err != nil {
		t.Fatal(err)
	}
	cw.until(turnEnded(3))
	ho := fresh()
	sess, err := d.store.Session(ctx, code.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ho.seen[code.ID] != sess.LastSeq {
		t.Fatalf("seen %d, last %d", ho.seen[code.ID], sess.LastSeq)
	}
	if q, err := d.store.Queue(ctx, parent.ID); err != nil || len(q) != 0 {
		t.Fatalf("archived parent's queue %+v %v", q, err)
	}
	if got := texts(); !slices.Equal(got, want) {
		t.Fatalf("reports to an archived parent %q", got)
	}
}

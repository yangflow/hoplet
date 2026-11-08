package daemon

import (
	"context"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// reportsOf returns the user messages in the session's log with a source.
func reportsOf(t *testing.T, st *store.Store, id proto.SessionID) []proto.Message {
	t.Helper()
	evs, err := st.Events(context.Background(), id, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.Message
	for _, ev := range evs {
		if ev.Type != proto.TypeMessage {
			continue
		}
		p, err := ev.Decode()
		if err != nil {
			t.Fatal(err)
		}
		if m := p.(proto.Message); m.Role == proto.RoleUser && m.From != "" {
			out = append(out, m)
		}
	}
	return out
}

func newChild(t *testing.T, d *Daemon, parent proto.SessionID) proto.Session {
	t.Helper()
	c, err := d.mgr.Create(timeout(t), session.CreateRequest{Mode: proto.ModePersonal, Harness: "fake", Title: "Look up", Parent: parent, Owner: proto.OwnerDelegated})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestReportsWaitForTheParentsTurn checks that a report to a busy parent
// waits in its queue and is delivered once when the turn ends, and that a
// report to an idle parent is delivered at once.
func TestReportsWaitForTheParentsTurn(t *testing.T) {
	echo := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn, echo, echo}})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	parent := create(t, c, h)
	child := newChild(t, d, parent.ID)
	w := watch(t, c, parent.ID, 0)
	w.next()
	if _, err := c.Send(ctx, proto.SendParams{Session: parent.ID, Text: "first", Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	if _, queued, err := d.store.Report(ctx, parent.ID, child.ID, proto.ReportDone, "r1", "found it"); err != nil || !queued {
		t.Fatalf("report %v %v", queued, err)
	}
	w.until(func(w *watcher) bool { return len(w.queue) == 1 && w.queue[0].From == child.ID })
	if err := c.Respond(ctx, proto.RespondParams{Session: parent.ID, Approval: "a1", Option: "once", Key: "a"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(2))
	want := `[Report from Hoplet session ` + string(child.ID) + ` "Look up"]` + "\n\nfound it"
	if got := w.texts(2); len(got) != 2 || got[0] != "found it" || got[1] != want {
		t.Fatalf("turn 2 messages %q", got)
	}
	w.until(func(w *watcher) bool { return len(w.queue) == 0 })
	if _, queued, err := d.store.Report(ctx, parent.ID, child.ID, proto.ReportDone, "r1", "found it"); err != nil || queued {
		t.Fatalf("report again %v %v", queued, err)
	}
	if _, _, err := d.store.Report(ctx, parent.ID, child.ID, proto.ReportDone, "r2", "more"); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(3))
	if got := w.texts(3); len(got) != 2 || got[0] != "more" {
		t.Fatalf("turn 3 messages %q", got)
	}
	if rs := reportsOf(t, d.store, parent.ID); len(rs) != 2 || rs[0].From != child.ID || rs[1].From != child.ID {
		t.Fatalf("reports %+v", rs)
	}
}

// TestReportsSurviveARestart checks that a report queued while no daemon
// runs is delivered by the next daemon, once.
func TestReportsSurviveARestart(t *testing.T) {
	h, script := newHome(t, echoScript)
	d, err := Start(context.Background(), testConfig(h, script))
	if err != nil {
		t.Fatal(err)
	}
	parent := create(t, dial(t, h, "device:a"), h)
	child := newChild(t, d, parent.ID)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if _, queued, err := st.Report(context.Background(), parent.ID, child.ID, proto.ReportDone, "r1", "found it"); err != nil || !queued {
		t.Fatalf("report %v %v", queued, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	d = start(t, testConfig(h, script))
	waitCond(t, "the report to be delivered", func() bool {
		q, err := d.store.Queue(context.Background(), parent.ID)
		return err == nil && len(q) == 0
	})
	c := dial(t, h, "device:a")
	watch(t, c, parent.ID, 0).until(turnEnded(1))
	if _, queued, err := d.store.Report(timeout(t), parent.ID, child.ID, proto.ReportDone, "r1", "found it"); err != nil || queued {
		t.Fatalf("report again %v %v", queued, err)
	}
	if rs := reportsOf(t, d.store, parent.ID); len(rs) != 1 || rs[0].Text != "found it" {
		t.Fatalf("reports %+v", rs)
	}
}

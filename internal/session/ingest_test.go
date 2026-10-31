package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func TestPrepareEntriesReplacesInvalidOnes(t *testing.T) {
	good := store.Entry{Payload: proto.Message{ID: "m", Role: proto.RoleAssistant, Text: "ok"}}
	in := []store.Entry{
		good,
		{Payload: proto.ApprovalResolved{ID: "a", Outcome: "maybe"}},
		{Payload: proto.TurnStarted{}},
		{},
	}
	out := prepareEntries(0, in)
	if len(out) != len(in) || out[0] != good {
		t.Fatalf("out %+v", out)
	}
	for i := 1; i < len(out); i++ {
		er, ok := out[i].Payload.(proto.Error)
		if !ok || !strings.HasPrefix(er.Message, "invalid harness event: ") {
			t.Fatalf("entry %d: %+v", i, out[i])
		}
		if err := store.ValidateEntry(0, out[i]); err != nil {
			t.Fatalf("replacement %d is invalid: %v", i, err)
		}
	}
	// The same batch stores in one append.
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if _, err := e.store.Append(context.Background(), store.AppendRequest{Session: sess.ID, Entries: out}); err != nil {
		t.Fatal(err)
	}
}

func TestNewTurnWithoutEndingFailsThePreviousOne(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "turn_started"}}, // a second start inside the turn
		{Emit: &fake.Ev{Ev: "message", ID: "m", Text: "late"}},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if _, err := m.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnEnded) == 2 })
	want := []proto.EventType{proto.TypeMessage, proto.TypeTurnStarted, proto.TypeTurnEnded, proto.TypeTurnStarted, proto.TypeMessage, proto.TypeTurnEnded}
	for i, ev := range evs {
		if ev.Type != want[i] {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	ps := payloads(t, evs)
	if first := ps[2].(proto.TurnEnded); first.Reason != proto.TurnFailed || evs[2].Turn != 1 {
		t.Fatalf("previous turn end %+v turn %d", first, evs[2].Turn)
	}
	if evs[3].Turn != 2 || evs[4].Turn != 2 || ps[5].(proto.TurnEnded).Reason != proto.TurnCompleted {
		t.Fatalf("second turn: %s", describe(evs))
	}
}

func TestStrayTurnEndIsDropped(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "turn_ended", Reason: "interrupted"}},
		{Emit: &fake.Ev{Ev: "turn_ended", Reason: "failed"}}, // not in a turn any more
		{Emit: &fake.Ev{Ev: "message", ID: "m", Text: "sentinel"}},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if _, err := m.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "sentinel", func(evs []proto.Event) bool {
		for _, p := range payloads(t, evs) {
			if msg, ok := p.(proto.Message); ok && msg.Text == "sentinel" {
				return true
			}
		}
		return false
	})
	e.waitOffsetAtSpoolEnd(m, sess.ID)
	evs, _ := e.store.Events(context.Background(), sess.ID, 0, 1000)
	if n := count(evs, proto.TypeTurnEnded); n != 1 {
		t.Fatalf("%d turn ends: %s", n, describe(evs))
	}
	for _, p := range payloads(t, evs) {
		if end, ok := p.(proto.TurnEnded); ok && end.Reason != proto.TurnInterrupted {
			t.Fatalf("the first end must stand: %+v", end)
		}
	}
}

func TestNativeSessionIsWrittenOnlyOnChange(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	in := &ingester{m: m, l: &live{id: sess.ID}}
	version := func() uint64 {
		v, err := e.store.CatalogVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := in.event(harness.Event{Native: "n1"}); err != nil {
		t.Fatal(err)
	}
	v := version()
	for range 3 {
		if err := in.event(harness.Event{Native: "n1"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := version(); got != v {
		t.Fatalf("catalog version moved %d -> %d on an unchanged native ID", v, got)
	}
	if got, _ := e.store.Session(ctx, sess.ID); got.Harness.NativeID != "n1" {
		t.Fatalf("native ID %q", got.Harness.NativeID)
	}
	if err := in.event(harness.Event{Native: "n2"}); err != nil || version() == v {
		t.Fatalf("a changed native ID must be written: %v", err)
	}
}

// failRun is a harness.Run whose Send fails.
type failRun struct{ harness.Run }

func (failRun) Send(context.Context, string) error { return errors.New("pipe closed") }

// recRun records what Send was given.
type recRun struct {
	harness.Run
	sent []string
}

func (r *recRun) Send(_ context.Context, text string) error {
	r.sent = append(r.sent, text)
	return nil
}

func TestDeliverFailureIsRecordedInTheMessageTurn(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	l := &live{id: sess.ID, run: failRun{}}
	if err := m.deliver(ctx, l, "x", SendResult{Seq: 1, Turn: 1}); err == nil {
		t.Fatal("send error swallowed")
	}
	if l.pending.Load() {
		t.Fatal("pending left set after a failed send")
	}
	evs, _ := e.store.Events(ctx, sess.ID, 0, 10)
	if len(evs) != 1 || evs[0].Type != proto.TypeError || evs[0].Turn != 1 || !strings.Contains(string(evs[0].Payload), "send failed: pipe closed") {
		t.Fatalf("events: %s", describe(evs))
	}
}

func TestResendOnlyWhenTheMessageNeverReachedTheHarness(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	res, err := e.store.Append(ctx, store.AppendRequest{
		Session: sess.ID, Turn: 1, ReceiptKey: "send:k",
		Entries: []store.Entry{{Payload: proto.Message{ID: "k", Role: proto.RoleUser, Text: "x"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := &recRun{}
	l := &live{id: sess.ID, run: rr}
	got, err := m.resend(ctx, l, "x", res, 1, false) // its turn started: done
	if err != nil || got.Turn != 1 || got.Seq != res.FirstSeq || len(rr.sent) != 0 {
		t.Fatalf("started turn: %+v %v sent %v", got, err, rr.sent)
	}
	if _, err := m.resend(ctx, l, "x", res, 0, false); err != nil || len(rr.sent) != 1 { // never started, nothing in flight
		t.Fatalf("resend: %v sent %v", err, rr.sent)
	}
	if !l.pending.Load() {
		t.Fatal("a resent message must be pending")
	}
	if _, err := m.resend(ctx, l, "x", res, 0, false); err != nil || len(rr.sent) != 1 { // in flight now
		t.Fatalf("second resend: %v sent %v", err, rr.sent)
	}
}

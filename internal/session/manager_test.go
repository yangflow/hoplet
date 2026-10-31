package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func intp(n int) *int { return &n }

func TestSendStreamsIntoStore(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "native-1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "hel"}},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "lo"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "hello"}},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if err := m.Send(context.Background(), sess.ID, "hi", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	want := []proto.EventType{proto.TypeMessage, proto.TypeTurnStarted, proto.TypeMessageDelta, proto.TypeMessageDelta, proto.TypeMessage, proto.TypeTurnEnded}
	if len(evs) != len(want) {
		t.Fatalf("events: %s", describe(evs))
	}
	for i, ev := range evs {
		if ev.Type != want[i] || ev.Turn != 1 || ev.Seq != uint64(i+1) {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	ps := payloads(t, evs)
	if u := ps[0].(proto.Message); u.Role != proto.RoleUser || u.Text != "hi" {
		t.Fatalf("user message %+v", u)
	}
	got, err := e.store.Session(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != proto.StatusIdle || got.Harness.NativeID != "native-1" {
		t.Fatalf("session %+v", got)
	}
	if off, _ := e.store.IngestOffset(context.Background(), sess.ID); off == 0 {
		t.Fatal("ingest offset not committed")
	}
}

func TestSendIsIdempotent(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "once", "k"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	if err := m.Send(ctx, sess.ID, "once", "k"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "settled", func(evs []proto.Event) bool { return turnEnded(evs) })
	if n := count(evs, proto.TypeTurnStarted); n != 1 {
		t.Fatalf("retry started %d turns: %s", n, describe(evs))
	}
	if err := m.Send(ctx, sess.ID, "", ""); err == nil {
		t.Fatal("send without key accepted")
	}
}

func TestSendWhileRunningIsBusy(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{AwaitApproval: "a"}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if err := m.Send(ctx, sess.ID, "more", "k2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("send during turn: %v", err)
	}
}

func TestApprovalFirstAnswerWins(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []fake.Option{{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "no", Label: "Deny", Scope: "deny"}}}},
		{AwaitApproval: "a1"},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, opt := range []string{"once", "no"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = m.Respond(ctx, sess.ID, "a1", opt, "device:"+opt, "r-"+opt)
		}()
	}
	wg.Wait()
	conflicts := 0
	for _, err := range errs {
		if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("errors %v: want exactly one conflict", errs)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if n := count(evs, proto.TypeApprovalResolved); n != 1 {
		t.Fatalf("%d resolutions: %s", n, describe(evs))
	}
}

func TestInterruptEndsTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{SleepMS: 60_000}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "slow", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if err := m.Interrupt(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnInterrupted {
		t.Fatalf("end %+v", end)
	}
}

func TestHarnessCrashAbortsTurnAndShimExits(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "partial"}},
		{Exit: intp(3)},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if err := m.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if n := count(evs, proto.TypeTurnEnded); n != 1 {
		t.Fatalf("%d turn ends: %s", n, describe(evs))
	}
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnAborted {
		t.Fatalf("end %+v", end)
	}
	got, _ := e.store.Session(context.Background(), sess.ID)
	if got.Status != proto.StatusErrored {
		t.Fatalf("status %s", got.Status)
	}
	waitShimGone(t, m, sess.ID) // the exit record was acked
}

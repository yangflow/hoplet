package session

import (
	"context"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// directFake is a DirectHarness for tests: it runs the fake binary as a
// plain child process, with no shim in between. It deliberately does not
// embed the fake harness, which would make it a StdioHarness too.
type directFake struct{ h *fake.Harness }

func (d directFake) Kind() string { return "fake" }

func (d directFake) Probe(ctx context.Context) (harness.Info, error) { return d.h.Probe(ctx) }

func (d directFake) Start(ctx context.Context, spec harness.Spec) (harness.Run, error) {
	l, err := d.h.Launch(spec)
	if err != nil {
		return nil, err
	}
	ex, err := harness.StartExec(ctx, l.Command, l.Env, l.Dir)
	if err != nil {
		return nil, err
	}
	run, err := d.h.Attach(ctx, spec, ex)
	if err != nil {
		_ = ex.Close()
		return nil, err
	}
	return run, nil
}

func (e *env) directManager() *Manager {
	e.t.Helper()
	return e.managerWith(map[string]harness.Harness{"direct": directFake{&fake.Harness{Binary: fakeExe, Script: e.script}}})
}

func (e *env) createDirect(m *Manager) proto.Session {
	e.t.Helper()
	sess, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "direct", Directory: e.work})
	if err != nil {
		e.t.Fatal(err)
	}
	return sess
}

func TestDirectHarnessSend(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "hi"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "hi"}},
	}}}})
	m := e.directManager()
	defer m.Close()
	sess := e.createDirect(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	want := []proto.EventType{proto.TypeMessage, proto.TypeTurnStarted, proto.TypeMessageDelta, proto.TypeMessage, proto.TypeTurnEnded}
	if len(evs) != len(want) {
		t.Fatalf("events: %s", describe(evs))
	}
	for i, ev := range evs {
		if ev.Type != want[i] {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	if got, _ := e.store.Session(ctx, sess.ID); got.Harness.NativeID != "n1" {
		t.Fatalf("native ID %q", got.Harness.NativeID)
	}
	if off, _ := e.store.IngestOffset(ctx, sess.ID); off != 0 {
		t.Fatalf("a direct harness has no spool offsets, got %d", off)
	}
	if c := shimCount(e.root); c != 0 {
		t.Fatalf("%d shims for a direct harness", c)
	}
	if err := m.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.attached(sess.ID); err == nil {
		t.Fatal("still attached after Stop")
	}
}

func TestDirectHarnessRecoverStartsOnlyOpenTurns(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", ResumeTurn: 1, Turns: []fake.Turn{
		{Steps: []fake.Step{{AwaitApproval: "a"}}}, {Steps: []fake.Step{{EchoPrompt: true}}},
	}})
	first := e.directManager()
	busy := e.createDirect(first)
	idle := e.createDirect(first)
	ctx := context.Background()
	if _, err := first.Send(ctx, busy.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(busy.ID, "turn start", started)
	if err := first.Close(); err != nil { // a direct harness ends with its owner
		t.Fatal(err)
	}
	second := e.directManager()
	defer second.Close()
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := second.attached(busy.ID); err != nil {
		t.Fatalf("the session with an open turn was not resumed: %v", err)
	}
	if _, err := second.attached(idle.ID); err == nil {
		t.Fatal("an idle direct session must start lazily")
	}
}

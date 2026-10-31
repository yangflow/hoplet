package session

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

var approvalScript = fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
	{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []fake.Option{
		{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "no", Label: "Deny", Scope: "deny"},
	}}},
	{AwaitApproval: "a1"},
}}}}

func resolved(t *testing.T, evs []proto.Event) proto.ApprovalResolved {
	t.Helper()
	for _, p := range payloads(t, evs) {
		if r, ok := p.(proto.ApprovalResolved); ok {
			return r
		}
	}
	t.Fatalf("no resolution in %s", describe(evs))
	return proto.ApprovalResolved{}
}

func TestRespondDenyIsDenied(t *testing.T) {
	e := newEnv(t, approvalScript)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	if err := m.Respond(ctx, sess.ID, "a1", "no", "device:1", "r1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	r := resolved(t, evs)
	if r.Outcome != proto.OutcomeDenied || r.OptionID != "no" || r.DecidedBy != "device:1" {
		t.Fatalf("resolution %+v", r)
	}
	for _, ev := range evs {
		if ev.Type == proto.TypeApprovalResolved && ev.Turn != 1 {
			t.Fatalf("decision recorded in turn %d, want the request's turn 1", ev.Turn)
		}
	}
}

func TestRespondUnknownOptionWritesNothing(t *testing.T) {
	e := newEnv(t, approvalScript)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	before := e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	if err := m.Respond(ctx, sess.ID, "a1", "always", "device:1", "r1"); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("got %v", err)
	}
	after, err := e.store.Events(ctx, sess.ID, 0, 1000)
	if err != nil || len(after) != len(before) {
		t.Fatalf("events changed: %d -> %d, %v", len(before), len(after), err)
	}
	if p, _ := e.store.PendingApprovals(ctx, sess.ID); len(p) != 1 {
		t.Fatalf("approval no longer pending: %v", p)
	}
	if err := m.Respond(ctx, sess.ID, "nope", "once", "device:1", "r2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown approval: %v", err)
	}
}

func TestRespondWhileNotRunningRecordsThenDeliversOnRetry(t *testing.T) {
	e := newEnv(t, approvalScript)
	first := e.manager()
	sess := e.create(first)
	ctx := context.Background()
	if _, err := first.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := e.manager()
	defer second.Close()
	if err := second.Respond(ctx, sess.ID, "a1", "once", "device:1", "r1"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("got %v", err)
	}
	evs, _ := e.store.Events(ctx, sess.ID, 0, 1000)
	if r := resolved(t, evs); r.Outcome != proto.OutcomeAllowed {
		t.Fatalf("decision not recorded: %+v", r)
	}
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Respond(ctx, sess.ID, "a1", "once", "device:1", "r1"); err != nil { // same key: delivers
		t.Fatal(err)
	}
	evs = e.waitFor(sess.ID, "turn end", turnEnded)
	if n := count(evs, proto.TypeApprovalResolved); n != 1 {
		t.Fatalf("%d resolutions: %s", n, describe(evs))
	}
	approved := 0
	for _, p := range payloads(t, evs) {
		if m, ok := p.(proto.Message); ok && m.Text == "approved:once" {
			approved++
		}
	}
	if approved != 1 {
		t.Fatalf("harness approved %d times: %s", approved, describe(evs))
	}
	// A third delivery after the turn ended is dropped quietly.
	if err := second.Respond(ctx, sess.ID, "a1", "once", "device:1", "r1"); err != nil {
		t.Fatal(err)
	}
}

package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// specSpy is the fake harness, recording the spec of each launch.
type specSpy struct {
	*fake.Harness
	mu    sync.Mutex
	specs []harness.Spec
}

func (s *specSpy) Launch(spec harness.Spec) (harness.Launch, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return s.Harness.Launch(spec)
}

func (s *specSpy) launched() []harness.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]harness.Spec(nil), s.specs...)
}

func TestCreateDefaultsAndFallback(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1"})
	ctx := context.Background()
	full := e.manager()
	defer full.Close()
	code := e.create(full)
	personal, err := full.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if code.Permission != proto.ModeAutoReview || personal.Permission != proto.ModeConfirm {
		t.Fatalf("defaults: code %s, personal %s", code.Permission, personal.Permission)
	}

	// A harness with only confirm and auto_edit, one effort level and no
	// plan mode.
	limited := e.managerWith(map[string]harness.Harness{"fake": &fake.Harness{Binary: fakeExe, Script: e.script, Capabilities: &proto.Capabilities{
		SetMode: true, Modes: []proto.PermissionMode{proto.ModeConfirm, proto.ModeAutoEdit}, Efforts: []proto.Effort{proto.EffortHigh},
	}}})
	defer limited.Close()
	sess, err := limited.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Effort: proto.EffortHigh})
	if err != nil || sess.Permission != proto.ModeAutoEdit || sess.Effort != proto.EffortHigh {
		t.Fatalf("fallback create %+v %v", sess, err)
	}
	if err := limited.SetPermission(ctx, sess.ID, proto.ModeFullAuto); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.Session(ctx, sess.ID); got.Permission != proto.ModeAutoEdit {
		t.Fatalf("full_auto on a harness without it stored %s", got.Permission)
	}
	for name, err := range map[string]error{
		"plan at create":   errOf(limited.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Plan: true})),
		"effort at create": errOf(limited.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Effort: proto.EffortMax})),
		"set plan":         limited.SetPlan(ctx, sess.ID, true),
		"set effort":       limited.SetEffort(ctx, sess.ID, proto.EffortLow),
	} {
		if !errors.Is(err, harness.ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", name, err)
		}
	}
	if _, err := full.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Permission: "read_only"}); err == nil {
		t.Error("the retired read_only mode was accepted")
	}
	if err := full.SetEffort(ctx, code.ID, "huge"); err == nil {
		t.Error("an unknown effort was accepted")
	}
}

func errOf(_ proto.Session, err error) error { return err }

func TestPlanAndEffortReachTheHarness(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{AwaitApproval: "hold"},
	}}}})
	spy := &specSpy{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	m := e.managerWith(map[string]harness.Harness{"fake": spy})
	defer m.Close()
	ctx := context.Background()
	sess, err := m.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Plan: true, Effort: proto.EffortMedium})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "the turn to start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if got := spy.launched(); len(got) != 1 || !got[0].Plan || got[0].Effort != proto.EffortMedium || got[0].Mode != proto.ModeAutoReview {
		t.Fatalf("launch specs %+v", got)
	}
	if err := m.SetPlan(ctx, sess.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEffort(ctx, sess.ID, proto.EffortXHigh); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "the harness to see both changes", func(evs []proto.Event) bool { return count(evs, proto.TypeMessage) >= 3 })
	var texts []string
	for _, p := range payloads(t, evs) {
		if msg, ok := p.(proto.Message); ok && msg.Role == proto.RoleAssistant {
			texts = append(texts, msg.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "plan:off" || texts[1] != "effort:xhigh" {
		t.Fatalf("harness messages %q", texts)
	}
	got, err := e.store.Session(ctx, sess.ID)
	if err != nil || got.Plan || got.Effort != proto.EffortXHigh {
		t.Fatalf("stored %+v %v", got, err)
	}
}

func TestHarnessModeChangeUpdatesTheSession(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "mode_changed", Text: "auto_edit"}},
	}}}})
	m := e.manager()
	defer m.Close()
	ctx := context.Background()
	sess, err := m.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	got, err := e.store.Session(ctx, sess.ID)
	if err != nil || got.Plan || got.Permission != proto.ModeAutoEdit {
		t.Fatalf("after the harness left plan mode %+v %v", got, err)
	}
}

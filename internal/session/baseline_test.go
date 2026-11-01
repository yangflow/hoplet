package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// baselineCalls records what Config.Baseline and the save it returns were
// asked.
type baselineCalls struct {
	mu       sync.Mutex
	captured []string // directories, in order
	saved    []proto.SessionID
	err      error // from the save
	block    bool  // capture waits for the context to end
	// seen is the store, whose sessions are listed at capture time.
	seen    *store.Store
	visible []int // sessions in the store at each capture
}

func (b *baselineCalls) fn(ctx context.Context, dir string) func(context.Context, proto.SessionID) error {
	b.mu.Lock()
	b.captured = append(b.captured, dir)
	b.mu.Unlock()
	if b.seen != nil {
		ss, _ := b.seen.Sessions(ctx)
		b.visible = append(b.visible, len(ss))
	}
	if b.block {
		<-ctx.Done()
	}
	return func(_ context.Context, id proto.SessionID) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.saved = append(b.saved, id)
		return b.err
	}
}

func (e *env) managerBaseline(b *baselineCalls) *Manager {
	e.t.Helper()
	m, err := New(Config{
		Store: e.store, Root: filepath.Join(e.root, "s"), Spawner: ExecSpawner{Exe: hopletExe},
		Harnesses: map[string]harness.Harness{"fake": &fake.Harness{Binary: fakeExe, Script: e.script}},
		Baseline:  b.fn,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func TestCreateCapturesTheBaselineBeforeTheSessionExists(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1"})
	b := &baselineCalls{seen: e.store}
	m := e.managerBaseline(b)
	defer m.Close()
	ctx := context.Background()
	code := e.create(m)
	personal, err := m.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.captured) != 2 || b.captured[0] != e.work || b.captured[1] != m.dir(personal.ID) {
		t.Fatalf("captured %v", b.captured)
	}
	// Nothing was visible when the baseline was captured; it was saved for
	// the sessions once stored.
	if len(b.visible) != 2 || b.visible[0] != 0 || b.visible[1] != 1 {
		t.Fatalf("sessions visible at capture: %v", b.visible)
	}
	if len(b.saved) != 2 || b.saved[0] != code.ID || b.saved[1] != personal.ID {
		t.Fatalf("saved %v", b.saved)
	}
	if evs, err := e.store.Events(ctx, code.ID, 0, 10); err != nil || len(evs) != 0 {
		t.Fatalf("events %v %v", evs, err)
	}
	if dir, err := m.WorkDir(ctx, code.ID); err != nil || dir != e.work {
		t.Fatalf("work dir %q %v", dir, err)
	}
	if dir, err := m.WorkDir(ctx, personal.ID); err != nil || dir != m.dir(personal.ID) {
		t.Fatalf("personal work dir %q %v", dir, err)
	}
	if _, err := m.WorkDir(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("work dir of an unknown session: %v", err)
	}
}

func TestBaselineFailureDoesNotFailCreate(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1"})
	b := &baselineCalls{err: errors.New("no git")}
	m := e.managerBaseline(b)
	defer m.Close()
	sess, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work})
	if err != nil || sess.ID == "" || len(b.saved) != 1 {
		t.Fatalf("create %+v %v after %d saves", sess, err, len(b.saved))
	}
	if _, err := e.store.Session(context.Background(), sess.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineHasADeadline(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1"})
	b := &baselineCalls{block: true}
	m := e.managerBaseline(b)
	defer m.Close()
	m.baselineTimeout = 50 * time.Millisecond
	start := time.Now()
	sess, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work})
	if err != nil || sess.ID == "" {
		t.Fatalf("create %+v %v", sess, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("create waited for the baseline")
	}
}

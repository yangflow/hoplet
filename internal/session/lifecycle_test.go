package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/shim"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

var longTurn = fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{SleepMS: 60_000}}}}}

func started(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 }

func TestStopEndsHarnessAndAcksExit(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Stop(ctx, sess.ID); err != nil { // not running: nothing to do
		t.Fatal(err)
	}
	if _, err := m.Send(ctx, sess.ID, "slow", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", started)
	if err := m.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnAborted {
		t.Fatalf("end %+v", end)
	}
	if count(evs, proto.TypeError) != 0 {
		t.Fatalf("a requested stop must not log an error: %s", describe(evs))
	}
	if _, err := m.attached(sess.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("still attached after Stop: %v", err)
	}
	waitShimGone(t, m, sess.ID) // the shim leaves only after the exit was acked
}

func TestArchiveStopsAndArchives(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "slow", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", started)
	if err := m.Archive(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	waitShimGone(t, m, sess.ID)
	got, err := e.store.Session(ctx, sess.ID)
	if err != nil || got.ArchivedAt == nil {
		t.Fatalf("not archived: %+v %v", got, err)
	}
	if _, err := m.Send(ctx, sess.ID, "more", "k2"); err == nil {
		t.Fatal("send to an archived session accepted")
	}
}

func TestRecoverStopsRunningArchivedSession(t *testing.T) {
	e := newEnv(t, longTurn)
	first := e.manager()
	sess := e.create(first)
	ctx := context.Background()
	if _, err := first.Send(ctx, sess.ID, "slow", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", started)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.store.ArchiveSession(ctx, sess.ID, time.Now()); err != nil { // archived while the daemon was away
		t.Fatal(err)
	}
	second := e.manager()
	defer second.Close()
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitShimGone(t, second, sess.ID)
	e.waitFor(sess.ID, "turn end", turnEnded)
}

func TestConcurrentSendStartsOneShim(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = m.Send(context.Background(), sess.ID, "go", "k"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrBusy):
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d sends succeeded, want 1: %v", ok, errs)
	}
	if c := shimCount(m.dir(sess.ID)); c != 1 {
		t.Fatalf("%d shims for one session", c)
	}
}

func TestSendAfterHarnessExitedBetweenTurnsRespawns(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", ResumeTurn: 1, Turns: []fake.Turn{
		{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}},
	}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "first turn end", turnEnded)
	if err := syscall.Kill(harnessPid(t, m.dir(sess.ID)), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "the exit note", func(evs []proto.Event) bool { return count(evs, proto.TypeError) == 1 })
	var msg string
	for _, p := range payloads(t, evs) {
		if er, ok := p.(proto.Error); ok {
			msg = er.Message
		}
	}
	if !strings.Contains(msg, "between turns") || !strings.Contains(msg, "SIGKILL") {
		t.Fatalf("exit note %q", msg)
	}
	waitShimGone(t, m, sess.ID)
	waitCond(t, "the live entry to go", func() bool { _, err := m.attached(sess.ID); return err != nil })
	if _, err := m.Send(ctx, sess.ID, "two", "k2"); err != nil {
		t.Fatal(err)
	}
	evs = e.waitFor(sess.ID, "second turn end", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnEnded) == 2 })
	if n := count(evs, proto.TypeTurnStarted); n != 2 {
		t.Fatalf("%d turns started: %s", n, describe(evs))
	}
	if got, _ := e.store.Session(ctx, sess.ID); got.Status != proto.StatusIdle {
		t.Fatalf("status %s", got.Status)
	}
}

// TestReattachAfterConnectionTaken checks that when another client takes over
// the shim, the manager reattaches and every event is stored exactly once.
func TestReattachAfterConnectionTaken(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "a"}},
		{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "t", Options: []fake.Option{{ID: "once", Label: "Once", Scope: "once"}}}},
		{AwaitApproval: "a1"},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "b"}},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	old, err := m.attached(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := shim.Dial(ctx, m.dir(sess.ID), 0) // takes the shim over from the manager
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	waitCond(t, "the manager to reattach", func() bool {
		l, err := m.attached(sess.ID)
		return err == nil && l != old
	})
	if err := m.Respond(ctx, sess.ID, "a1", "once", "device:1", "r1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	for typ, want := range map[proto.EventType]int{
		proto.TypeTurnStarted: 1, proto.TypeMessageDelta: 2, proto.TypeApprovalRequest: 1,
		proto.TypeApprovalResolved: 1, proto.TypeTurnEnded: 1, proto.TypeError: 0,
	} {
		if got := count(evs, typ); got != want {
			t.Fatalf("%d %s events, want %d: %s", got, typ, want, describe(evs))
		}
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq gap: %s", describe(evs))
		}
	}
}

func TestIngestAppendFailureReattachesWithoutLoss(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "a"}},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "b"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "ab"}},
	}}}})
	m := e.manager()
	defer m.Close()
	var failed atomic.Bool
	real := m.appendFn
	m.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		for _, en := range req.Entries {
			if _, ok := en.Payload.(proto.TurnStarted); ok && !failed.Swap(true) {
				return store.AppendResult{}, errors.New("disk full")
			}
		}
		return real(ctx, req)
	}
	sess := e.create(m)
	if _, err := m.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if !failed.Load() {
		t.Fatal("the failure was never injected")
	}
	for typ, want := range map[proto.EventType]int{
		proto.TypeMessage: 2, proto.TypeTurnStarted: 1, proto.TypeMessageDelta: 2, proto.TypeTurnEnded: 1, proto.TypeError: 1,
	} {
		if got := count(evs, typ); got != want {
			t.Fatalf("%d %s events, want %d: %s", got, typ, want, describe(evs))
		}
	}
	for _, p := range payloads(t, evs) {
		if er, ok := p.(proto.Error); ok && !strings.HasPrefix(er.Message, "ingest failed: disk full") {
			t.Fatalf("error note %q", er.Message)
		}
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq gap: %s", describe(evs))
		}
	}
	e.waitOffsetAtSpoolEnd(m, sess.ID)
}

func TestIngestBatchesEvents(t *testing.T) {
	steps := make([]fake.Step, 60)
	for i := range steps {
		steps[i] = fake.Step{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "x"}}
	}
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: steps}}})
	m := e.manager()
	defer m.Close()
	var appends atomic.Int32
	real := m.appendFn
	m.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		appends.Add(1)
		return real(ctx, req)
	}
	sess := e.create(m)
	if _, err := m.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	if n := appends.Load(); n > 20 {
		t.Fatalf("%d appends for ~65 events: no batching", n)
	}
}

func TestUnreachableShimIsRecordedNotSpawnedOver(t *testing.T) {
	e := newEnv(t, longTurn)
	first := e.manager()
	sess := e.create(first)
	ctx := context.Background()
	if _, err := first.Send(ctx, sess.ID, "slow", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", started)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Claim the spool was read far beyond its end: the shim answers bad_offset.
	far := int64(1 << 30)
	if _, err := e.store.Append(ctx, store.AppendRequest{Session: sess.ID, IngestOffset: &far}); err != nil {
		t.Fatal(err)
	}
	second := e.manager()
	defer second.Close()
	if _, err := second.Send(ctx, sess.ID, "more", "k2"); err == nil {
		t.Fatal("send succeeded with an unusable spool")
	}
	if c := shimCount(second.dir(sess.ID)); c != 1 {
		t.Fatalf("%d shims: a failing attach must not spawn another", c)
	}
	evs := e.waitFor(sess.ID, "the attach error", func(evs []proto.Event) bool { return count(evs, proto.TypeError) == 1 })
	msg := payloads(t, evs)[len(evs)-1].(proto.Error).Message
	if !strings.Contains(msg, "cannot attach to the harness") || !strings.Contains(msg, "archive") {
		t.Fatalf("error note %q", msg)
	}
	if _, open, _ := e.store.TurnState(ctx, sess.ID); !open {
		t.Fatal("the turn must stay open")
	}
	if err := second.Recover(ctx); err != nil { // same failure through Recover
		t.Fatal(err)
	}
	if _, open, _ := e.store.TurnState(ctx, sess.ID); !open {
		t.Fatal("Recover aborted a turn whose shim is alive")
	}
}

// unsupportable reports itself unsupported once flipped.
type unsupportable struct {
	*fake.Harness
	off atomic.Bool
}

func (u *unsupportable) Probe(ctx context.Context) (harness.Info, error) {
	info, err := u.Harness.Probe(ctx)
	info.Supported = !u.off.Load()
	return info, err
}

func TestVersionGateRefusesSpawn(t *testing.T) {
	e := newEnv(t, longTurn)
	h := &unsupportable{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	m := e.managerWith(map[string]harness.Harness{"fake": h})
	defer m.Close()
	sess := e.create(m)
	h.off.Store(true)
	if _, err := m.Send(context.Background(), sess.ID, "go", "k1"); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("send: %v", err)
	}
	if c := shimCount(m.dir(sess.ID)); c != 0 {
		t.Fatalf("%d shims started for an unsupported harness", c)
	}
	if _, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("create: %v", err)
	}
}

func TestSlowShimStartIncludesLogTail(t *testing.T) {
	e := newEnv(t, longTurn)
	m, err := New(Config{
		Store: e.store, Root: filepath.Join(e.root, "s"), Spawner: ExecSpawner{Exe: "/bin/sh"}, // fails: "shim" is no script
		Harnesses: map[string]harness.Harness{"fake": &fake.Harness{Binary: fakeExe, Script: e.script}},
		Env:       []string{"PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.ready = 300 * time.Millisecond
	sess := e.create(m)
	_, err = m.Send(context.Background(), sess.ID, "go", "k1")
	if err == nil || !strings.Contains(err.Error(), "shim.log:") {
		t.Fatalf("error without the shim log: %v", err)
	}
}

func TestSendAfterCloseFails(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	sess := e.create(m)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(context.Background(), sess.ID, "go", "k1"); err == nil {
		t.Fatal("send on a closed manager accepted")
	}
	if c := shimCount(m.dir(sess.ID)); c != 0 {
		t.Fatalf("%d shims started by a closed manager", c)
	}
}

func TestCreateRemovesDirOnStoreError(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	_, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Permission: "bogus"})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(e.root, "s"))
	if len(entries) != 0 {
		t.Fatalf("leftover session directories: %v", entries)
	}
}

func TestSteerAnswerAndSetPermission(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "question", ID: "q1", Text: "which?"}},
		{AwaitAnswer: "q1"},
		{SleepMS: 60_000},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Steer(ctx, sess.ID, "x"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("steer while not running: %v", err)
	}
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "question", func(evs []proto.Event) bool { return count(evs, proto.TypeQuestion) == 1 })
	if err := m.Answer(ctx, sess.ID, "q1", "yes"); err != nil {
		t.Fatal(err)
	}
	if err := m.Steer(ctx, sess.ID, "left"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPermission(ctx, sess.ID, proto.ModeAutoEdit); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "the three echoes", func(evs []proto.Event) bool { return count(evs, proto.TypeMessage) >= 4 })
	var texts []string
	for _, p := range payloads(t, evs) {
		if msg, ok := p.(proto.Message); ok && msg.Role == proto.RoleAssistant {
			texts = append(texts, msg.Text)
		}
	}
	if got := strings.Join(texts, ","); got != "answered:yes,steer:left,mode:"+string(proto.ModeAutoEdit) {
		t.Fatalf("harness saw %q", got)
	}
	if got, _ := e.store.Session(ctx, sess.ID); got.Permission != proto.ModeAutoEdit {
		t.Fatalf("stored permission %q", got.Permission)
	}
}

func TestSetPermissionRefusedWithoutCapability(t *testing.T) {
	e := newEnv(t, longTurn)
	h := &fake.Harness{Binary: fakeExe, Script: e.script, Capabilities: &proto.Capabilities{}}
	m := e.managerWith(map[string]harness.Harness{"fake": h})
	defer m.Close()
	sess := e.create(m)
	if err := m.SetPermission(context.Background(), sess.ID, proto.ModeAutoEdit); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	if got, _ := e.store.Session(context.Background(), sess.ID); got.Permission != proto.ModeConfirm {
		t.Fatalf("permission written despite the refusal: %q", got.Permission)
	}
}

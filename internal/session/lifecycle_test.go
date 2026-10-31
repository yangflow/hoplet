package session

import (
	"context"
	"errors"
	"fmt"
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

func spoolCount(t *testing.T, dir, needle string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), needle)
}

func hasTurnStarted(req store.AppendRequest) bool {
	for _, en := range req.Entries {
		if _, ok := en.Payload.(proto.TurnStarted); ok {
			return true
		}
	}
	return false
}

// TestRetryWhileCatchingUpSendsNothing restarts the manager after the
// harness got the message and started its turn but before that was ingested.
// The retry must wait for ingestion to catch up instead of sending again.
func TestRetryWhileCatchingUpSendsNothing(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{AwaitApproval: "a"}}}}})
	first := e.manager()
	first.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		if hasTurnStarted(req) {
			return store.AppendResult{}, errors.New("disk full")
		}
		return first.cfg.Store.Append(ctx, req)
	}
	sess := e.create(first)
	ctx := context.Background()
	want, err := first.Send(ctx, sess.ID, "go", "k1")
	if err != nil {
		t.Fatal(err)
	}
	dir := first.dir(sess.ID)
	waitCond(t, "the harness to start its turn", func() bool { return spoolCount(t, dir, "turn_started") == 1 })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, open, _ := e.store.TurnState(ctx, sess.ID); open {
		t.Fatal("the turn start was ingested; the test needs it lost")
	}

	second := e.manager()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	defer second.Close()
	second.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		<-gate
		return second.cfg.Store.Append(ctx, req)
	}
	for _, key := range []string{"k1", "k2"} {
		_, err := second.Send(ctx, sess.ID, "go", key)
		if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "catching up") {
			t.Fatalf("send %s while catching up: %v", key, err)
		}
	}
	if n := spoolCount(t, dir, `"k":"stdin"`); n != 1 {
		t.Fatalf("the harness was sent %d messages", n)
	}
	release()
	e.waitFor(sess.ID, "the turn start", started)
	got, err := second.Send(ctx, sess.ID, "go", "k1")
	if err != nil || got != want {
		t.Fatalf("retry after catching up: %+v %v, want %+v", got, err, want)
	}
	if n := spoolCount(t, dir, `"k":"stdin"`); n != 1 {
		t.Fatalf("the harness was sent %d messages", n)
	}
}

// TestExitBatchCommitsAbortWithOffset fails the append carrying the exit once.
// The abort and the exit offset travel in one append, so after the reattach
// replays the exit the turn is aborted exactly once and the shim is acked.
func TestExitBatchCommitsAbortWithOffset(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "partial"}},
		{Exit: intp(3)},
	}}}})
	m := e.manager()
	defer m.Close()
	var failed atomic.Bool
	var failedReq store.AppendRequest
	real := m.appendFn
	m.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		for _, en := range req.Entries {
			if _, ok := en.Payload.(proto.TurnEnded); ok && !failed.Swap(true) {
				failedReq = req
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
	if failedReq.IngestOffset == nil {
		t.Fatal("the abort was appended without the exit's offset")
	}
	if n := count(evs, proto.TypeTurnEnded); n != 1 {
		t.Fatalf("%d turn ends: %s", n, describe(evs))
	}
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnAborted {
		t.Fatalf("end %+v", end)
	}
	waitShimGone(t, m, sess.ID) // acked after the replayed exit committed
	e.waitOffsetAtSpoolEnd(m, sess.ID)
}

func TestArchiveDuringReattachWindow(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	var failed atomic.Bool
	real := m.appendFn
	m.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		if hasTurnStarted(req) && !failed.Swap(true) {
			return store.AppendResult{}, errors.New("disk full")
		}
		return real(ctx, req)
	}
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	waitCond(t, "the failure", failed.Load) // the reattach is now pending or running
	if err := m.Archive(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	waitShimGone(t, m, sess.ID)
	if err := m.Close(); err != nil { // waits for the reattacher, which must give up
		t.Fatal(err)
	}
	if got, _ := e.store.Session(ctx, sess.ID); got.ArchivedAt == nil {
		t.Fatal("not archived")
	}
	m.mu.Lock()
	lives := len(m.live)
	_, hasLock := m.locks[sess.ID]
	m.mu.Unlock()
	if lives != 0 {
		t.Fatal("something attached after the archive")
	}
	if hasLock {
		t.Fatal("the archived session's lock entry was kept")
	}
}

func TestReattachRefusesArchivedSessions(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := e.store.ArchiveSession(ctx, sess.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := m.tryReattach(sess.ID, ""); err != nil { // no shim exists: an attach attempt would fail
		t.Fatalf("tryReattach on an archived session: %v", err)
	}
	if _, err := m.ensure(ctx, sess.ID); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("ensure on an archived session: %v", err)
	}
}

var quietScript = fake.Script{QuietStart: true, Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}}

// sendWhenCaughtUp sends until ingestion has caught up with the shim; any
// error other than ErrBusy fails the test.
func sendWhenCaughtUp(t *testing.T, m *Manager, id proto.SessionID, key string) {
	t.Helper()
	waitCond(t, "catching up to end", func() bool {
		_, err := m.Send(context.Background(), id, "go", key)
		if err != nil && !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		return err == nil
	})
}

// TestQuietHarnessCatchesUpAfterRestart: a harness that writes nothing until
// it gets stdin leaves only the shim's start record in the spool. A new
// manager must still count itself caught up, or Send would say "catching up"
// for ever.
func TestQuietHarnessCatchesUpAfterRestart(t *testing.T) {
	e := newEnv(t, quietScript)
	first := e.manager()
	sess := e.create(first)
	ctx := context.Background()
	if _, err := first.ensure(ctx, sess.ID); err != nil { // spawned, nothing written to stdin
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := e.manager()
	defer second.Close()
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	sendWhenCaughtUp(t, second, sess.ID, "k1")
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if n := count(evs, proto.TypeTurnStarted); n != 1 {
		t.Fatalf("%d turns: %s", n, describe(evs))
	}
}

// TestSpawnedShimThatWasNotAttachedInTime: the readiness wait gives up although
// the shim comes up; the next Send attaches to it and must not be stuck.
func TestSpawnedShimThatWasNotAttachedInTime(t *testing.T) {
	e := newEnv(t, quietScript)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	m.ready = time.Nanosecond
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err == nil {
		t.Fatal("send succeeded although the shim could not be reached in time")
	}
	waitCond(t, "the shim to come up", func() bool {
		c, err := shim.Dial(ctx, m.dir(sess.ID), 0)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	m.ready = readyTimeout
	sendWhenCaughtUp(t, m, sess.ID, "k1")
	e.waitFor(sess.ID, "turn end", turnEnded)
}

// flakyAttach fails its next few Attach calls.
type flakyAttach struct {
	*fake.Harness
	failures atomic.Int32
}

func (f *flakyAttach) Attach(ctx context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	if f.failures.Add(-1) >= 0 {
		return nil, errors.New("attach glitch")
	}
	return f.Harness.Attach(ctx, spec, io)
}

func TestTransientReattachErrorsRetryAndAreRecordedOnce(t *testing.T) {
	e := newEnv(t, approvalScript)
	h := &flakyAttach{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	m := e.managerWith(map[string]harness.Harness{"fake": h})
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
	h.failures.Store(3)
	c, err := shim.Dial(ctx, m.dir(sess.ID), 0) // takes the shim over; the next three reattaches fail
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
	if n := count(evs, proto.TypeError); n != 1 {
		t.Fatalf("%d attach errors recorded, want one per streak: %s", n, describe(evs))
	}
	if n := count(evs, proto.TypeApprovalRequest); n != 1 {
		t.Fatalf("%d approval requests: %s", n, describe(evs))
	}
}

// TestReattachGivesUpOnBadOffset: a shim that rejects the committed offset
// will keep rejecting it, so the reattacher records that once and stops.
func TestReattachGivesUpOnBadOffset(t *testing.T) {
	e := newEnv(t, approvalScript)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	far := int64(1 << 30)
	if _, err := e.store.Append(ctx, store.AppendRequest{Session: sess.ID, IngestOffset: &far}); err != nil {
		t.Fatal(err)
	}
	c, err := shim.Dial(ctx, m.dir(sess.ID), 0) // takes the shim over: the manager reattaches with the far offset
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }() // returns only when the reattacher has ended
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the reattacher kept retrying a bad offset")
	}
	evs, _ := e.store.Events(ctx, sess.ID, 0, 1000)
	if n := count(evs, proto.TypeError); n != 1 {
		t.Fatalf("%d errors recorded: %s", n, describe(evs))
	}
	if _, open, _ := e.store.TurnState(ctx, sess.ID); !open {
		t.Fatal("the turn must stay open")
	}
}

func TestPermanentAttachErrors(t *testing.T) {
	for _, err := range []error{
		&shim.Error{Code: shim.CodeBadOffset}, &shim.Error{Code: shim.CodeVersion},
		fmt.Errorf("x: %w", ErrUnknownHarness),
	} {
		if !permanentAttachError(err) {
			t.Errorf("%v should be permanent", err)
		}
	}
	for _, err := range []error{errNoShim, errors.New("glitch"), &shim.Error{Code: shim.CodeCorrupt}} {
		if permanentAttachError(err) {
			t.Errorf("%v should be retried", err)
		}
	}
}

package session

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func (r *recRun) Close() error { return nil }

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
	got, err := m.resend(ctx, l, "x", res, sendState{last: 1, synced: true}) // its turn started: done
	if err != nil || got.Turn != 1 || got.Seq != res.FirstSeq || len(rr.sent) != 0 {
		t.Fatalf("started turn: %+v %v sent %v", got, err, rr.sent)
	}
	if _, err := m.resend(ctx, l, "x", res, sendState{synced: true}); err != nil || len(rr.sent) != 1 { // never started, nothing in flight
		t.Fatalf("resend: %v sent %v", err, rr.sent)
	}
	if !l.pending.Load() {
		t.Fatal("a resent message must be pending")
	}
	if _, err := m.resend(ctx, l, "x", res, sendState{synced: true, pending: l.pending.Load()}); err != nil || len(rr.sent) != 1 { // in flight now
		t.Fatalf("second resend: %v sent %v", err, rr.sent)
	}
	if _, err := m.resend(ctx, &live{id: sess.ID, run: rr}, "x", res, sendState{}); !errors.Is(err, ErrBusy) || len(rr.sent) != 1 { // not caught up
		t.Fatalf("resend while catching up: %v sent %v", err, rr.sent)
	}
}

// TestSendReadsPendingBeforeTurnState replays the interleaving that used to
// double-send: the turn's start is committed (and pending cleared) between
// Send's two reads. Reading pending first makes Send see either the pending
// flag or the committed turn, never neither.
func TestSendReadsPendingBeforeTurnState(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	rr := &recRun{}
	l := &live{id: sess.ID, run: rr}
	l.pending.Store(true) // a message is in flight
	m.mu.Lock()
	m.live[sess.ID] = l
	m.mu.Unlock()
	m.afterPendingRead = func() {
		// What ingestion does once the turn has started: commit, then clear.
		if _, err := e.store.Append(ctx, store.AppendRequest{Session: sess.ID, Turn: 1, Entries: []store.Entry{{Payload: proto.TurnStarted{}}}}); err != nil {
			t.Error(err)
		}
		l.pending.Store(false)
	}
	if _, err := m.Send(ctx, sess.ID, "second", "k2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if len(rr.sent) != 0 {
		t.Fatalf("sent %v during a running turn", rr.sent)
	}
	// Once the turn has ended the same call goes through.
	m.afterPendingRead = nil
	if _, err := e.store.Append(ctx, store.AppendRequest{Session: sess.ID, Turn: 1, Entries: []store.Entry{{Payload: proto.TurnEnded{Reason: proto.TurnCompleted}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(ctx, sess.ID, "second", "k2"); err != nil || len(rr.sent) != 1 {
		t.Fatalf("send after the turn: %v sent %v", err, rr.sent)
	}
}

// endedRun is a run whose event stream ends with an adapter error.
type endedRun struct {
	harness.Run
	events chan harness.Event
	err    error
}

func (r *endedRun) Events() <-chan harness.Event { return r.events }
func (r *endedRun) Err() error                   { return r.err }
func (r *endedRun) Close() error                 { return nil }

func TestStreamEndWithoutExitAbortsOpenTurn(t *testing.T) {
	e := newEnv(t, longTurn)
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := e.store.Append(ctx, store.AppendRequest{Session: sess.ID, Turn: 1, Entries: []store.Entry{{Payload: proto.TurnStarted{}}}}); err != nil {
		t.Fatal(err)
	}
	run := &endedRun{events: make(chan harness.Event), err: errors.New("adapter broke")}
	l := &live{id: sess.ID, run: run, done: make(chan struct{})}
	m.mu.Lock()
	m.live[sess.ID] = l
	m.wg.Add(1)
	m.mu.Unlock()
	go m.ingest(l, 1, true)
	close(run.events)
	<-l.done
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnAborted {
		t.Fatalf("end %+v", end)
	}
}

func TestIngestBackoffDoublesAndCaps(t *testing.T) {
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond, 3200 * time.Millisecond, 5 * time.Second, 5 * time.Second}
	for i, w := range want {
		if got := ingestBackoff(i); got != w {
			t.Errorf("streak %d: %v, want %v", i, got, w)
		}
	}
	if got := ingestBackoff(1 << 20); got != 5*time.Second {
		t.Errorf("huge streak: %v", got)
	}
}

// TestPersistentAppendFailureRecordsOneNote fails the turn-start batch
// several times in a row: every event still arrives exactly once and the
// failure is noted once for the whole streak.
func TestPersistentAppendFailureRecordsOneNote(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "a"}},
	}}}})
	m := e.manager()
	defer m.Close()
	var failures atomic.Int32
	real := m.appendFn
	m.appendFn = func(ctx context.Context, req store.AppendRequest) (store.AppendResult, error) {
		for _, en := range req.Entries {
			if _, ok := en.Payload.(proto.TurnStarted); ok && failures.Add(1) <= 3 {
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
	for typ, want := range map[proto.EventType]int{proto.TypeTurnStarted: 1, proto.TypeMessageDelta: 1, proto.TypeError: 1} {
		if got := count(evs, typ); got != want {
			t.Fatalf("%d %s events, want %d: %s", got, typ, want, describe(evs))
		}
	}
	if failures.Load() != 4 {
		t.Fatalf("turn-start batch tried %d times, want 3 failures and 1 success", failures.Load())
	}
}

func TestRetryDeliversTheRecordedDecision(t *testing.T) {
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
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	// The retry names another option; what was decided is what gets delivered.
	if err := second.Respond(ctx, sess.ID, "a1", "no", "device:1", "r1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	delivered := ""
	for _, p := range payloads(t, evs) {
		if m, ok := p.(proto.Message); ok && strings.HasPrefix(m.Text, "approved:") {
			delivered = m.Text
		}
	}
	if delivered != "approved:once" {
		t.Fatalf("harness was given %q", delivered)
	}
}

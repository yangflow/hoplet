package extract

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// fakeHarness answers extractions from replies, repeating the last one.
type fakeHarness struct {
	mu      sync.Mutex
	calls   []harness.ExtractRequest
	replies []string
	err     error
	block   chan struct{} // holds Extract when set
	entered chan struct{}
	models  []proto.ModelInfo
}

func (f *fakeHarness) Kind() string { return "claude" }
func (f *fakeHarness) Probe(context.Context) (harness.Info, error) {
	return harness.Info{Version: "1", Supported: true, Models: f.models}, nil
}
func (f *fakeHarness) ExtractSupport(harness.Info) string { return "" }
func (f *fakeHarness) Extract(ctx context.Context, r harness.ExtractRequest) (harness.ExtractResult, error) {
	if f.block != nil {
		close(f.entered)
		select {
		case <-f.block:
		case <-ctx.Done():
			return harness.ExtractResult{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	if f.err != nil {
		return harness.ExtractResult{}, f.err
	}
	reply := `{"ops":[]}`
	if len(f.replies) > 0 {
		reply = f.replies[0]
		if len(f.replies) > 1 {
			f.replies = f.replies[1:]
		}
	}
	return harness.ExtractResult{Output: reply, Usage: proto.Usage{InputTokens: 1}}, nil
}

func (f *fakeHarness) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// plainHarness cannot extract.
type plainHarness struct{}

func (plainHarness) Kind() string { return "hermes" }
func (plainHarness) Probe(context.Context) (harness.Info, error) {
	return harness.Info{Version: "1", Supported: true}, nil
}

// refusingHarness has an unverified version.
type refusingHarness struct{ fakeHarness }

func (*refusingHarness) ExtractSupport(harness.Info) string { return "not verified" }

var start = time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

type rig struct {
	t   *testing.T
	st  *store.Store
	mem *memory.Service
	h   *fakeHarness
	w   *Worker
	now time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st := newStore(t)
	mem, err := memory.Open(context.Background(), memory.Config{Root: t.TempDir(), Store: st, Sessions: st})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, st: st, mem: mem, h: &fakeHarness{}, now: start}
	r.w = r.worker(map[string]harness.Harness{"claude": r.h, "hermes": plainHarness{}, "old": &refusingHarness{}})
	return r
}

func (r *rig) worker(hs map[string]harness.Harness) *Worker {
	return New(Config{
		Store: r.st, Memory: r.mem, Harnesses: hs, Now: func() time.Time { return r.now },
		Scopes: func(context.Context, proto.Session) ([]memory.Scope, error) {
			return []memory.Scope{{Kind: memory.ScopeGlobal}, {Kind: memory.ScopeRepo, Value: "repo1"}}, nil
		},
	})
}

// session adds a session whose one user message is at the rig's start.
func (r *rig) session(id proto.SessionID, kind, model string) uint64 {
	r.t.Helper()
	sess := proto.Session{ID: id, Harness: proto.HarnessRef{Kind: kind}, Model: model}
	if _, err := r.st.CreateSession(context.Background(), withDefaults(sess)); err != nil {
		r.t.Fatal(err)
	}
	return put(r.t, r.st, id, start, "", said("I like tea"))
}

func withDefaults(s proto.Session) proto.Session {
	s.Mode, s.Owner, s.Origin, s.CreatedAt = proto.ModePersonal, proto.OwnerMe, proto.OriginMe, start.Add(-time.Hour)
	return s
}

func (r *rig) progress(id proto.SessionID) uint64 {
	r.t.Helper()
	seq, err := r.st.MemoryExtractionProgress(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return seq
}

func (r *rig) runs() int {
	r.t.Helper()
	n, err := r.st.CountMemoryExtractions(context.Background(), "", start.AddDate(-1, 0, 0), start.AddDate(1, 0, 0))
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *rig) bodies() []string {
	r.t.Helper()
	es, err := r.mem.Search(context.Background(), store.MemoryQuery{Status: string(memory.StatusActive)})
	if err != nil {
		r.t.Fatal(err)
	}
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Body
	}
	return out
}

func createOp(event uint64, scope, body string) string {
	return `{"ops":[{"action":"create","category":"preference","scope":"` + scope + `","body":"` + body +
		`","evidence":[{"kind":"user_stated","event":` + strconv.FormatUint(event, 10) + `}],"reason":"said so"}]}`
}

func TestIdleScan(t *testing.T) {
	r := newRig(t)
	seq := r.session("s", "claude", "m1")
	r.now = start.Add(IdleAfter - time.Second)
	r.w.cycle(context.Background())
	if r.h.count() != 0 {
		t.Fatalf("ran before idle: %d", r.h.count())
	}
	r.h.replies = []string{createOp(seq, "global", "Likes tea")}
	r.now = start.Add(IdleAfter)
	r.w.cycle(context.Background())
	if r.h.count() != 1 || r.h.calls[0].Model != "m1" || !strings.Contains(r.h.calls[0].Input, `user: "I like tea"`) {
		t.Fatalf("calls = %+v", r.h.calls)
	}
	if got := r.bodies(); len(got) != 1 || got[0] != "Likes tea" {
		t.Fatalf("entries = %q", got)
	}
	if r.progress("s") != seq {
		t.Fatalf("progress = %d", r.progress("s"))
	}
	r.w.cycle(context.Background())
	if r.h.count() != 1 {
		t.Fatal("ran again with nothing new")
	}
}

func (r *rig) settings(change func(*proto.ProfileSettings)) {
	r.t.Helper()
	ctx := context.Background()
	set, err := r.st.MemorySettings(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	change(&set)
	if err := r.st.SetMemorySettings(ctx, set); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) lastRun(id proto.SessionID) store.MemoryExtraction {
	r.t.Helper()
	run, err := r.st.LastMemoryExtraction(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return run
}

func TestAutoSaveOffMakesCandidates(t *testing.T) {
	r := newRig(t)
	seq := r.session("s", "claude", "m1")
	r.settings(func(s *proto.ProfileSettings) { s.AutoSave = false })
	r.h.replies = []string{createOp(seq, "global", "Likes tea")}
	r.w.triggers["s"] = TriggerArchive
	r.w.cycle(context.Background())
	if got := r.bodies(); len(got) != 0 {
		t.Fatalf("saved with auto-save off: %q", got)
	}
	got, err := r.mem.Search(context.Background(), store.MemoryQuery{Status: string(memory.StatusCandidate)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "Likes tea" {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestCodeSessionsWaitForConsent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	sess := withDefaults(proto.Session{ID: "c", Harness: proto.HarnessRef{Kind: "claude"}, Model: "m1"})
	sess.Mode = proto.ModeCode
	if _, err := r.st.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	old := put(t, r.st, "c", start, "", said("I like tea"))
	r.w.triggers["c"] = TriggerArchive
	r.w.cycle(ctx)
	if r.h.count() != 0 || r.progress("c") != old {
		t.Fatalf("calls = %d, progress = %d", r.h.count(), r.progress("c"))
	}
	r.settings(func(s *proto.ProfileSettings) { s.IncludeCode = true })
	r.w.triggers["c"] = TriggerArchive
	r.w.cycle(ctx)
	if r.h.count() != 0 {
		t.Fatalf("earlier records reached the model: %d calls", r.h.count())
	}
	seq := put(t, r.st, "c", start.Add(time.Minute), "", said("I like coffee"))
	r.h.replies = []string{createOp(seq, "global", "Likes coffee")}
	r.w.triggers["c"] = TriggerArchive
	r.w.cycle(ctx)
	if r.h.count() != 1 || strings.Contains(r.h.calls[0].Input, "I like tea") || !strings.Contains(r.h.calls[0].Input, "I like coffee") {
		t.Fatalf("calls = %+v", r.h.calls)
	}
}

func TestSpecifiedHarness(t *testing.T) {
	tests := []struct {
		name    string
		models  []proto.ModelInfo
		runs    int
		problem bool
	}{
		{"default model", []proto.ModelInfo{{ID: "m2"}, {ID: "m3", Default: true}}, 1, false},
		{"no default model", []proto.ModelInfo{{ID: "m2"}}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			ctx := context.Background()
			seq := r.session("s", "hermes", "")
			r.h.models = tt.models
			r.h.replies = []string{createOp(seq, "global", "Likes tea")}
			r.settings(func(s *proto.ProfileSettings) { s.ExtractionHarness = "claude" })
			r.w.triggers["s"] = TriggerArchive
			r.w.cycle(ctx)
			if r.runs() != tt.runs {
				t.Fatalf("runs = %d, want %d", r.runs(), tt.runs)
			}
			if tt.runs == 1 {
				if run := r.lastRun("s"); run.Harness != "claude" || run.Model != "m3" {
					t.Fatalf("run = %s %s", run.Harness, run.Model)
				}
			}
			st, err := r.w.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(st.Harnesses, func(h proto.ProfileExtractionHarness) bool { return h.Kind == "claude" })
			if (st.Harnesses[i].Unsupported != "") != tt.problem {
				t.Fatalf("status = %+v", st.Harnesses[i])
			}
		})
	}
}

func TestCheckHarness(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for kind, want := range map[string]bool{"": true, "claude": true, "hermes": false, "missing": false} {
		err := r.w.CheckHarness(ctx, kind)
		if (err == nil) != want || (err != nil && !errors.Is(err, store.ErrInvalid)) {
			t.Errorf("CheckHarness(%q) = %v", kind, err)
		}
	}
}

func TestTriggerSkipsIdleWait(t *testing.T) {
	r := newRig(t)
	r.session("s", "claude", "m1")
	r.w.triggers["s"] = TriggerArchive
	r.w.cycle(context.Background())
	if r.h.count() != 1 {
		t.Fatalf("calls = %d", r.h.count())
	}
	last, err := r.st.LastMemoryExtraction(context.Background(), "s")
	if err != nil || last.Trigger != TriggerArchive || last.State != store.ExtractionDone {
		t.Fatalf("last = %+v, %v", last, err)
	}
}

func TestNoRun(t *testing.T) {
	cases := []struct {
		name, kind, model string
		off               bool
	}{
		{name: "no extractor", kind: "hermes", model: "m1"},
		{name: "unverified version", kind: "old", model: "m1"},
		{name: "model not reported", kind: "claude"},
		{name: "switched off", kind: "claude", model: "m1", off: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.session("s", c.kind, c.model)
			if c.off {
				if err := r.w.SetEnabled(context.Background(), false); err != nil {
					t.Fatal(err)
				}
			}
			r.now = start.Add(time.Hour)
			r.w.cycle(context.Background())
			if r.runs() != 0 || r.progress("s") != 0 {
				t.Fatalf("runs = %d, progress = %d", r.runs(), r.progress("s"))
			}
		})
	}
}

func TestStatusNamesUnsupported(t *testing.T) {
	r := newRig(t)
	st, err := r.w.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, h := range st.Harnesses {
		why[h.Kind] = h.Unsupported
	}
	if !st.Enabled || st.DailyLimit != DailyLimit || why["claude"] != "" || why["hermes"] == "" || why["old"] != "not verified" {
		t.Fatalf("status = %+v", st)
	}
}

func TestNothingToReadAdvances(t *testing.T) {
	r := newRig(t)
	if _, err := r.st.CreateSession(context.Background(), withDefaults(proto.Session{ID: "s", Harness: proto.HarnessRef{Kind: "claude"}, Model: "m1"})); err != nil {
		t.Fatal(err)
	}
	seq := put(t, r.st, "s", start, "", proto.Error{Message: "boom"})
	r.now = start.Add(time.Hour)
	r.w.cycle(context.Background())
	if r.h.count() != 0 || r.progress("s") != seq {
		t.Fatalf("calls = %d, progress = %d", r.h.count(), r.progress("s"))
	}
}

func TestBudgetKeepsMaterial(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for i := range DailyLimit {
		run, err := r.st.CreateMemoryExtraction(ctx, store.MemoryExtraction{Session: proto.SessionID("o" + strconv.Itoa(i)), Harness: "claude", Model: "m", From: 1, To: 1, Trigger: "idle"}, start)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.st.FailMemoryExtraction(ctx, run.ID, "x", start); err != nil {
			t.Fatal(err)
		}
	}
	r.session("s", "claude", "m1")
	r.now = start.Add(time.Hour)
	r.w.cycle(ctx)
	if r.h.count() != 0 || r.progress("s") != 0 {
		t.Fatalf("over budget: calls = %d, progress = %d", r.h.count(), r.progress("s"))
	}
	r.now = start.AddDate(0, 0, 1)
	r.w.cycle(ctx)
	if r.h.count() != 1 || r.progress("s") == 0 {
		t.Fatalf("next day: calls = %d, progress = %d", r.h.count(), r.progress("s"))
	}
}

func TestBadOutputRetriesThenFails(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	seq := r.session("s", "claude", "m1")
	r.h.replies = []string{"not json"}
	r.now = start.Add(time.Hour)
	for range maxAttempts + 2 {
		r.w.cycle(ctx)
	}
	if r.h.count() != maxAttempts {
		t.Fatalf("calls = %d", r.h.count())
	}
	last, err := r.st.LastMemoryExtraction(ctx, "s")
	if err != nil || last.State != store.ExtractionFailed || r.progress("s") != 0 {
		t.Fatalf("last = %+v, progress = %d, %v", last, r.progress("s"), err)
	}
	if st, _ := r.w.Status(ctx); st.Failed != 1 {
		t.Fatalf("status = %+v", st)
	}
	r.h.replies = []string{`{"ops":[]}`}
	r.w.triggers["s"] = TriggerReport
	r.w.cycle(ctx)
	if last, _ = r.st.LastMemoryExtraction(ctx, "s"); last.State != store.ExtractionDone || last.From != 1 || r.progress("s") != seq {
		t.Fatalf("after trigger: %+v", last)
	}
}

func TestHarnessErrorRetries(t *testing.T) {
	r := newRig(t)
	r.session("s", "claude", "m1")
	r.h.err = errors.New("no network")
	r.now = start.Add(time.Hour)
	r.w.cycle(context.Background())
	last, err := r.st.LastMemoryExtraction(context.Background(), "s")
	if err != nil || last.State != store.ExtractionPending || last.Attempts != 1 || last.Error != "no network" {
		t.Fatalf("last = %+v, %v", last, err)
	}
}

func openRun(t *testing.T, r *rig, id proto.SessionID, seq uint64) store.MemoryExtraction {
	t.Helper()
	run, err := r.st.CreateMemoryExtraction(context.Background(), store.MemoryExtraction{Session: id, Harness: "claude", Model: "m1", From: 1, To: seq, Trigger: "idle"}, start)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCrashWhileCalling(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	seq := r.session("s", "claude", "m1")
	run := openRun(t, r, "s", seq)
	if err := r.st.StartMemoryExtraction(ctx, run.ID, start); err != nil {
		t.Fatal(err)
	}
	r.h.replies = []string{createOp(seq, "global", "Likes tea")}
	if err := r.w.recoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	r.w.cycle(ctx)
	r.w.cycle(ctx)
	if r.h.count() != 1 || len(r.bodies()) != 1 {
		t.Fatalf("calls = %d, entries = %v", r.h.count(), r.bodies())
	}
	if last, _ := r.st.LastMemoryExtraction(ctx, "s"); last.Attempts != 2 {
		t.Fatalf("attempts = %d", last.Attempts)
	}
}

func TestCrashAfterParsing(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	seq := r.session("s", "claude", "m1")
	run := openRun(t, r, "s", seq)
	output := createOp(seq, "global", "Likes tea")
	if err := r.st.SetMemoryExtractionParsed(ctx, run.ID, output, proto.Usage{}, start); err != nil {
		t.Fatal(err)
	}
	// The crash came after the commit of the op and before the run was
	// marked done, the worst place for it.
	ops, err := Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	scopes := []memory.Scope{{Kind: memory.ScopeGlobal}}
	write, why, err := r.w.writeOf(ctx, run, scopes, 0, ops[0])
	if err != nil || why != "" {
		t.Fatal(why, err)
	}
	if _, err := r.mem.Apply(ctx, write); err != nil {
		t.Fatal(err)
	}
	if err := r.w.recoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	r.w.cycle(ctx)
	if r.h.count() != 0 || len(r.bodies()) != 1 || r.progress("s") != seq {
		t.Fatalf("calls = %d, entries = %v, progress = %d", r.h.count(), r.bodies(), r.progress("s"))
	}
}

func TestCorrectionNotRevived(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	seq := r.session("s", "claude", "m1")
	created, err := r.mem.Apply(ctx, memory.Write{OpID: "u1", Actor: memory.ActorUser, Action: memory.ActionCreate, Category: memory.CategoryPreference, Scope: memory.Scope{Kind: memory.ScopeGlobal}, Body: "Likes coffee now"})
	if err != nil || created.Outcome != memory.OutcomeApplied {
		t.Fatalf("create = %+v, %v", created, err)
	}
	r.h.replies = []string{`{"ops":[{"action":"update","target":"` + created.Entry.ID + `","version":1,"body":"Likes tea","evidence":[{"kind":"user_stated","event":` + strconv.FormatUint(seq, 10) + `}],"reason":"old"}]}`}
	r.w.triggers["s"] = TriggerArchive
	r.now = time.Now().Add(time.Hour)
	r.w.cycle(ctx)
	if got := r.bodies(); len(got) != 1 || got[0] != "Likes coffee now" {
		t.Fatalf("entries = %q", got)
	}
}

func TestOpsRefusedKeepTheRest(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	seq := r.session("s", "claude", "m1")
	n := strconv.FormatUint(seq, 10)
	ev := `"evidence":[{"kind":"user_stated","event":` + n + `}],"reason":"r"`
	r.h.replies = []string{`{"ops":[
		{"action":"create","category":"fact","scope":"companion","body":"no such scope",` + ev + `},
		{"action":"update","target":"elsewhere","version":1,"body":"out of scope",` + ev + `},
		{"action":"create","category":"fact","scope":"global","body":"bad cite","evidence":[{"kind":"user_stated","event":999}],"reason":"r"},
		{"action":"create","category":"fact","scope":"repo","body":"kept",` + ev + `}
	]}`}
	r.w.triggers["s"] = TriggerArchive
	r.w.cycle(ctx)
	got := r.bodies()
	if len(got) != 1 || got[0] != "kept" {
		t.Fatalf("entries = %q", got)
	}
	es, err := r.mem.Search(ctx, store.MemoryQuery{Status: string(memory.StatusActive)})
	if err != nil || es[0].Scope != (memory.Scope{Kind: memory.ScopeRepo, Value: "repo1"}) {
		t.Fatalf("scope = %+v, %v", es, err)
	}
}

func TestEnqueueDoesNotWaitForARun(t *testing.T) {
	r := newRig(t)
	r.h.block, r.h.entered = make(chan struct{}), make(chan struct{})
	r.session("s", "claude", "m1")
	r.session("t", "claude", "m1")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.w.Run(ctx)
	}()
	r.w.Enqueue("s", TriggerArchive)
	<-r.h.entered
	// The worker is inside the harness call: these return all the same.
	r.w.Enqueue("t", TriggerArchive)
	put(t, r.st, "s", start, "", said("still talking"))
	if _, err := r.w.Status(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
}

func TestSavedEventIsNotEvidence(t *testing.T) {
	r := newRig(t)
	seq := r.session("s", "claude", "m1")
	r.h.replies = []string{createOp(seq, "global", "Likes tea")}
	r.now = start.Add(IdleAfter)
	r.w.cycle(context.Background())
	sess, err := r.st.Session(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if r.h.count() != 1 || sess.LastSeq != seq+1 {
		t.Fatalf("calls = %d, last seq = %d", r.h.count(), sess.LastSeq)
	}
	saved, err := r.st.EventBySeq(context.Background(), "s", sess.LastSeq)
	if err != nil || saved.Type != proto.TypeMemorySaved {
		t.Fatalf("last event = %+v, %v", saved, err)
	}

	r.now = r.now.Add(2 * IdleAfter)
	r.w.cycle(context.Background())
	if r.h.count() != 1 || r.runs() != 1 {
		t.Fatalf("calls = %d, runs = %d", r.h.count(), r.runs())
	}

	next := put(t, r.st, "s", r.now, "", said("I like coffee"))
	r.h.replies = []string{createOp(next, "global", "Likes coffee")}
	r.now = r.now.Add(IdleAfter)
	r.w.cycle(context.Background())
	if r.h.count() != 2 || !strings.Contains(r.h.calls[1].Input, "coffee") || strings.Contains(r.h.calls[1].Input, "memory") {
		t.Fatalf("calls = %+v", r.h.calls)
	}
	if r.progress("s") != next {
		t.Fatalf("progress = %d, want %d", r.progress("s"), next)
	}
}

func TestSavedEventDoesNotDelayIdle(t *testing.T) {
	r := newRig(t)
	seq := r.session("s", "claude", "m1")
	saved := proto.MemorySaved{Entry: "e1", Version: 1, Action: proto.MemoryCreated, Actor: proto.MemoryByCompanion}
	if err := r.st.AppendMemorySaved(context.Background(), "s", start.Add(20*time.Minute), saved, "k"); err != nil {
		t.Fatal(err)
	}
	r.h.replies = []string{createOp(seq, "global", "Likes tea")}
	r.now = start.Add(IdleAfter)
	r.w.cycle(context.Background())
	if r.h.count() != 1 {
		t.Fatalf("calls = %d", r.h.count())
	}
}

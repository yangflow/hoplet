package extract

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// The defaults of background extraction, and what starts a run.
const (
	DailyLimit  = 20
	IdleAfter   = 30 * time.Minute
	maxAttempts = 3
	scanEvery   = time.Minute
	callTimeout = 10 * time.Minute

	TriggerArchive = "archive"
	TriggerHandoff = "handoff"
	TriggerReport  = "report"
	triggerIdle    = "idle"
)

// Config configures a Worker.
type Config struct {
	Store     *store.Store
	Memory    *memory.Service
	Harnesses map[string]harness.Harness
	// Scopes answers what a session's memory applies to.
	Scopes func(ctx context.Context, sess proto.Session) ([]memory.Scope, error)
	// Env returns the environment harnesses start in.
	Env func() []string
	Log *slog.Logger
	Now func() time.Time // time.Now when nil
}

// Worker runs extraction in the background, one run at a time.
type Worker struct {
	cfg  Config
	log  *slog.Logger
	now  func() time.Time
	wake chan struct{}

	mu       sync.Mutex
	triggers map[proto.SessionID]string
}

// New returns a Worker; Run starts it.
func New(cfg Config) *Worker {
	w := &Worker{cfg: cfg, log: cfg.Log, now: cfg.Now, wake: make(chan struct{}, 1), triggers: map[proto.SessionID]string{}}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	if w.now == nil {
		w.now = time.Now
	}
	return w
}

// Enqueue asks for a run over the session's new evidence and returns at
// once. A request lost to a crash is picked up by the idle scan.
func (w *Worker) Enqueue(id proto.SessionID, trigger string) {
	w.mu.Lock()
	w.triggers[id] = trigger
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run works until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	if err := w.recoverRuns(ctx); err != nil && ctx.Err() == nil {
		w.log.Error("recover extraction runs", "err", err)
	}
	t := time.NewTicker(scanEvery)
	defer t.Stop()
	for {
		w.cycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.wake:
		}
	}
}

// recoverRuns returns runs a crash left calling the harness to pending. A
// run past that point keeps its reply and only replays its commit.
func (w *Worker) recoverRuns(ctx context.Context) error {
	runs, err := w.cfg.Store.OpenMemoryExtractions(ctx)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.State != store.ExtractionRunning {
			continue
		}
		if err := w.cfg.Store.RetryMemoryExtraction(ctx, r.ID, "interrupted", w.now()); err != nil {
			return err
		}
	}
	return nil
}

// cycle creates the runs that are due and works through the open ones.
func (w *Worker) cycle(ctx context.Context) {
	w.mu.Lock()
	triggers := w.triggers
	w.triggers = map[proto.SessionID]string{}
	w.mu.Unlock()
	on, err := w.cfg.Store.MemoryExtractionEnabled(ctx)
	if err != nil {
		w.logErr(ctx, "read extraction switch", "", err)
		return
	}
	if !on {
		return
	}
	if err := w.scan(ctx, triggers); err != nil {
		w.logErr(ctx, "scan for extraction", "", err)
	}
	runs, err := w.cfg.Store.OpenMemoryExtractions(ctx)
	if err != nil {
		w.logErr(ctx, "list extraction runs", "", err)
		return
	}
	for _, r := range runs {
		if ctx.Err() != nil {
			return
		}
		if err := w.execute(ctx, r); err != nil {
			w.logErr(ctx, "run extraction", r.Session, err)
		}
	}
}

func (w *Worker) logErr(ctx context.Context, msg string, id proto.SessionID, err error) {
	if ctx.Err() == nil {
		w.log.Error(msg, "session", id, "err", err)
	}
}

// supportOf returns the harness's extractor, or why it has none.
func (w *Worker) supportOf(ctx context.Context, kind string) (harness.Extractor, string) {
	h, ok := w.cfg.Harnesses[kind]
	if !ok {
		return nil, "harness " + kind + " is not available"
	}
	ex, ok := h.(harness.Extractor)
	if !ok {
		return nil, "this harness cannot run an isolated extraction"
	}
	info, err := h.Probe(ctx)
	if err != nil {
		return nil, err.Error()
	}
	if why := ex.ExtractSupport(info); why != "" {
		return nil, why
	}
	return ex, ""
}

// day returns the local day around t.
func day(t time.Time) (from, to time.Time) {
	from = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return from, from.AddDate(0, 0, 1)
}

// scan creates a run for each session with new evidence that a trigger or
// its idleness calls for.
func (w *Worker) scan(ctx context.Context, triggers map[proto.SessionID]string) error {
	sessions, err := w.cfg.Store.Sessions(ctx)
	if err != nil {
		return err
	}
	supported := map[string]bool{}
	for _, s := range sessions {
		if s.Model == "" || s.LastSeq == 0 || (s.Mode != proto.ModePersonal && s.Mode != proto.ModeCode) {
			continue
		}
		through, err := w.cfg.Store.MemoryExtractionProgress(ctx, s.ID)
		if err != nil {
			return err
		}
		if s.LastSeq <= through {
			continue
		}
		// A saved memory is this worker's own echo, not new evidence.
		last, err := w.cfg.Store.LastSeqExcept(ctx, s.ID, proto.TypeMemorySaved)
		if err != nil {
			return err
		}
		if last <= through {
			continue
		}
		trigger, ok := triggers[s.ID]
		if !ok {
			if trigger, err = w.idle(ctx, s, last); err != nil {
				return err
			}
			if trigger == "" {
				continue
			}
		}
		can, known := supported[s.Harness.Kind]
		if !known {
			_, why := w.supportOf(ctx, s.Harness.Kind)
			can, supported[s.Harness.Kind] = why == "", why == ""
		}
		if !can {
			continue
		}
		if err := w.plan(ctx, s, through, last, trigger); err != nil {
			w.logErr(ctx, "plan extraction", s.ID, err)
		}
	}
	return nil
}

// idle returns triggerIdle when the session has been quiet since event seq
// long enough and has something a failed run has not already given up on.
func (w *Worker) idle(ctx context.Context, s proto.Session, seq uint64) (string, error) {
	if s.Status != proto.StatusIdle && s.ArchivedAt == nil {
		return "", nil
	}
	last, err := w.cfg.Store.EventBySeq(ctx, s.ID, seq)
	if err != nil {
		return "", err
	}
	if w.now().Sub(last.Time) < IdleAfter {
		return "", nil
	}
	prev, err := w.cfg.Store.LastMemoryExtraction(ctx, s.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return "", err
	case prev.State == store.ExtractionFailed && !last.Time.After(prev.UpdatedAt):
		return "", nil
	}
	return triggerIdle, nil
}

// plan creates the session's next run over the events after through up to
// last, unless there is nothing to read or the day's budget is spent, which
// leaves the material for later.
func (w *Worker) plan(ctx context.Context, s proto.Session, through, last uint64, trigger string) error {
	m, err := Select(ctx, w.cfg.Store, s.ID, through+1, last)
	if err != nil {
		return err
	}
	if m.Text == "" {
		return w.cfg.Store.AdvanceMemoryExtraction(ctx, s.ID, m.To)
	}
	now := w.now()
	from, to := day(now)
	used, err := w.cfg.Store.CountMemoryExtractions(ctx, s.Harness.Kind, from, to)
	if err != nil {
		return err
	}
	if used >= DailyLimit {
		return nil
	}
	_, err = w.cfg.Store.CreateMemoryExtraction(ctx, store.MemoryExtraction{
		Session: s.ID, Harness: s.Harness.Kind, Model: s.Model, From: through + 1, To: m.To, Trigger: trigger,
	}, now)
	if errors.Is(err, store.ErrExists) {
		return nil
	}
	return err
}

// execute takes one run as far as it goes now: one call of the harness at
// most, then the commit.
func (w *Worker) execute(ctx context.Context, r store.MemoryExtraction) error {
	sess, err := w.cfg.Store.Session(ctx, r.Session)
	if errors.Is(err, store.ErrNotFound) {
		return w.cfg.Store.FailMemoryExtraction(ctx, r.ID, "the session is gone", w.now())
	}
	if err != nil {
		return err
	}
	scopes, err := w.cfg.Scopes(ctx, sess)
	if err != nil {
		return err
	}
	if r.State != store.ExtractionParsed {
		var parsed bool
		if r, parsed, err = w.call(ctx, r, scopes); err != nil || !parsed {
			return err
		}
	}
	return w.commit(ctx, r, scopes)
}

// call asks the harness once. It reports whether the run now has a reply
// that parsed; a bad attempt is retried on a later cycle.
func (w *Worker) call(ctx context.Context, r store.MemoryExtraction, scopes []memory.Scope) (store.MemoryExtraction, bool, error) {
	if r.Attempts >= maxAttempts {
		reason := r.Error
		if reason == "" {
			reason = "out of attempts"
		}
		return r, false, w.cfg.Store.FailMemoryExtraction(ctx, r.ID, reason, w.now())
	}
	ex, why := w.supportOf(ctx, r.Harness)
	if why != "" {
		return r, false, w.cfg.Store.FailMemoryExtraction(ctx, r.ID, why, w.now())
	}
	m, err := Select(ctx, w.cfg.Store, r.Session, r.From, r.To)
	if err != nil {
		return r, false, err
	}
	existing, err := w.cfg.Memory.InScope(ctx, scopes, store.MemoryQuery{})
	if err != nil {
		return r, false, err
	}
	if err := w.cfg.Store.StartMemoryExtraction(ctx, r.ID, w.now()); err != nil {
		return r, false, err
	}
	r.Attempts++
	res, err := w.run(ctx, ex, r.Model, input(scopes, existing, m.Text))
	if err == nil {
		_, err = Parse(res.Output)
	}
	if err != nil {
		if ctx.Err() != nil {
			return r, false, ctx.Err() // recovered on the next start
		}
		if r.Attempts >= maxAttempts {
			return r, false, w.cfg.Store.FailMemoryExtraction(ctx, r.ID, err.Error(), w.now())
		}
		return r, false, w.cfg.Store.RetryMemoryExtraction(ctx, r.ID, err.Error(), w.now())
	}
	if err := w.cfg.Store.SetMemoryExtractionParsed(ctx, r.ID, res.Output, res.Usage, w.now()); err != nil {
		return r, false, err
	}
	r.Output, r.Usage, r.State = res.Output, res.Usage, store.ExtractionParsed
	return r, true, nil
}

// run calls the harness in an empty directory of its own.
func (w *Worker) run(ctx context.Context, ex harness.Extractor, model, in string) (harness.ExtractResult, error) {
	dir, err := os.MkdirTemp("", "hoplet-extract-")
	if err != nil {
		return harness.ExtractResult{}, fmt.Errorf("extract: work directory: %w", err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var env []string
	if w.cfg.Env != nil {
		env = w.cfg.Env()
	}
	return ex.Extract(ctx, harness.ExtractRequest{Env: env, Dir: dir, Model: model, System: system, Input: in})
}

// commit puts a parsed reply through the memory rules. Operation IDs come
// from the run, so replaying after a crash writes nothing twice.
func (w *Worker) commit(ctx context.Context, r store.MemoryExtraction, scopes []memory.Scope) error {
	ops, err := Parse(r.Output)
	if err != nil { // it parsed when it was stored
		return w.cfg.Store.FailMemoryExtraction(ctx, r.ID, err.Error(), w.now())
	}
	for i, op := range ops {
		write, why, err := w.writeOf(ctx, r, scopes, i, op)
		if err != nil {
			return err
		}
		if why != "" {
			w.log.Warn("extraction op rejected", "run", r.ID, "op", i, "why", why)
			continue
		}
		res, err := w.cfg.Memory.Apply(ctx, write)
		if errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrNotFound) {
			w.log.Warn("extraction op rejected", "run", r.ID, "op", i, "err", err)
			continue
		}
		if err != nil {
			return err
		}
		if res.Outcome == memory.OutcomeRejected {
			w.log.Info("extraction op rejected", "run", r.ID, "op", i, "code", res.Code, "reason", res.Reason)
		}
	}
	return w.cfg.Store.FinishMemoryExtraction(ctx, r, w.now())
}

// writeOf turns a model's op into a Write, adding what only the daemon may
// decide: the session behind the evidence and the value of the scope. A
// non-empty reason says why the op is refused.
func (w *Worker) writeOf(ctx context.Context, r store.MemoryExtraction, scopes []memory.Scope, i int, op Op) (memory.Write, string, error) {
	out := memory.Write{
		OpID: fmt.Sprintf("extract_%d_%d", r.ID, i), Actor: memory.ActorExtraction, Action: op.Action, Target: op.Target,
		ExpectedVersion: op.Version, Category: op.Category, Body: op.Body, Reason: op.Reason,
		Batch: &memory.Batch{Session: r.Session, From: r.From, To: r.To},
	}
	for _, c := range op.Evidence {
		out.Evidence = append(out.Evidence, memory.Evidence{Kind: c.Kind, Ref: fmt.Sprintf("event:%s/%d", r.Session, c.Event)})
	}
	if op.Scope != "" {
		i := slices.IndexFunc(scopes, func(s memory.Scope) bool { return s.Kind == op.Scope })
		if i < 0 {
			return memory.Write{}, "the session has no " + string(op.Scope) + " scope", nil
		}
		out.Scope = scopes[i]
	}
	if op.Target != "" {
		_, err := w.cfg.Memory.GetInScope(ctx, scopes, op.Target)
		if errors.Is(err, store.ErrNotFound) {
			return memory.Write{}, "entry " + op.Target + " is outside the session's scopes", nil
		}
		if err != nil {
			return memory.Write{}, "", err
		}
	}
	return out, "", nil
}

// Status says how extraction stands today.
func (w *Worker) Status(ctx context.Context) (proto.ProfileExtractionStatus, error) {
	on, err := w.cfg.Store.MemoryExtractionEnabled(ctx)
	if err != nil {
		return proto.ProfileExtractionStatus{}, err
	}
	from, to := day(w.now())
	out := proto.ProfileExtractionStatus{Enabled: on, DailyLimit: DailyLimit, Harnesses: []proto.ProfileExtractionHarness{}}
	if out.Failed, err = w.cfg.Store.CountFailedMemoryExtractions(ctx, from, to); err != nil {
		return proto.ProfileExtractionStatus{}, err
	}
	for _, kind := range slices.Sorted(maps.Keys(w.cfg.Harnesses)) {
		_, why := w.supportOf(ctx, kind)
		used, err := w.cfg.Store.CountMemoryExtractions(ctx, kind, from, to)
		if err != nil {
			return proto.ProfileExtractionStatus{}, err
		}
		out.Harnesses = append(out.Harnesses, proto.ProfileExtractionHarness{Kind: kind, Used: used, Unsupported: why})
	}
	return out, nil
}

// SetEnabled turns extraction on or off.
func (w *Worker) SetEnabled(ctx context.Context, on bool) error {
	return w.cfg.Store.SetMemoryExtractionEnabled(ctx, on)
}

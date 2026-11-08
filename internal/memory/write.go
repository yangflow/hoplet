package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// Action is what a write does to memory.
type Action string

// The actions.
const (
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionWithdraw Action = "withdraw"
)

// Batch is the range of one session's log an extraction read; From and To
// are inclusive.
type Batch struct {
	Session  proto.SessionID
	From, To uint64
}

// Write is one requested change from the user, a companion or extraction.
type Write struct {
	OpID            string
	Actor           Actor
	Action          Action
	Target          string // the entry to update or withdraw
	ExpectedVersion int
	Category        Category // required on create
	Scope           Scope    // required on create; the zero value keeps the target's
	Body            string
	Evidence        []Evidence
	Reason          string
	Session         proto.SessionID // the writing session of a companion
	Batch           *Batch          // the material of an extraction
}

// source is the session whose records may back this write.
func (w Write) source() proto.SessionID {
	if w.Batch != nil {
		return w.Batch.Session
	}
	return w.Session
}

// Outcome says what became of a write. Only OutcomeApplied may be reported
// as saved.
type Outcome string

// The outcomes.
const (
	OutcomeApplied   Outcome = "applied"
	OutcomeCandidate Outcome = "candidate"
	OutcomeRejected  Outcome = "rejected"
)

// Result is what became of a write that the rules decided. Entry is empty
// when it was rejected; Code and Reason then say why.
type Result struct {
	Outcome Outcome
	Entry   Entry
	Code    string
	Reason  string
}

// Apply runs one write through the memory rules. Refusals by those rules are
// audited and returned as an OutcomeRejected Result; the error is for bad
// requests, version conflicts and infrastructure. Repeating an OpID returns
// the first result and writes nothing.
func (s *Service) Apply(ctx context.Context, w Write) (Result, error) {
	if w.OpID == "" {
		return Result{}, invalid("a write needs an operation ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.apply(ctx, w)
	if err == nil {
		s.reportSaved(ctx, w, res)
	}
	return res, err
}

// reportSaved puts an automatic save in its source session's log. The entry
// is already on disk, so a failure here costs only the line in the chat. A
// retried OpID that failed here reports it then, once.
func (s *Service) reportSaved(ctx context.Context, w Write, res Result) {
	var saved proto.MemorySaved
	switch w.Action {
	case ActionCreate:
		saved.Action = proto.MemoryCreated
	case ActionUpdate:
		saved.Action = proto.MemoryUpdated
	default:
		return
	}
	switch w.Actor {
	case ActorCompanion:
		saved.Actor = proto.MemoryByCompanion
	case ActorExtraction:
		saved.Actor = proto.MemoryByExtraction
	default:
		return
	}
	if res.Outcome != OutcomeApplied || w.source() == "" {
		return
	}
	saved.Entry, saved.Version = res.Entry.ID, res.Entry.Version
	err := s.at("report")
	if err == nil {
		err = s.store.AppendMemorySaved(ctx, w.source(), time.Now(), saved, "memory_saved:"+w.OpID)
	}
	if err != nil {
		s.log.Warn("report memory save", "op", w.OpID, "session", w.source(), "err", err)
	}
}

func (s *Service) apply(ctx context.Context, w Write) (Result, error) {
	if res, done, err := s.earlierResult(ctx, w); done {
		return res, err
	}
	w.Body = strings.TrimSpace(w.Body)
	w.Reason = strings.TrimSpace(w.Reason)
	if err := w.checkShape(); err != nil {
		return Result{}, err
	}
	if w.Actor == ActorCompanion {
		sess, err := s.sessions.Session(ctx, w.Session)
		if err != nil {
			return Result{}, err
		}
		if sess.Mode != proto.ModePersonal {
			return Result{}, invalid("only a Personal session can write memory")
		}
	}
	if hasCredential(w.Body) || hasCredential(w.Reason) {
		return s.reject(ctx, w, CodeCredential, "the text looks like it holds a credential", true)
	}
	var times []time.Time
	if w.Actor == ActorUser {
		w.Evidence = []Evidence{{Kind: EvidenceUserStated, Ref: "op:" + w.OpID}}
	} else {
		var problem string
		var err error
		times, problem, err = s.checkEvidence(ctx, w)
		if err != nil {
			return Result{}, err
		}
		if problem != "" {
			return s.reject(ctx, w, CodeBadEvidence, problem, false)
		}
	}
	var row *store.MemoryRow
	conflict := false
	if w.Action != ActionCreate {
		r, err := s.store.MemoryIndexEntry(ctx, w.Target)
		if err != nil {
			return Result{}, err
		}
		if err := w.checkTarget(r); err != nil {
			return Result{}, err
		}
		row = &r
		err = s.checkHead(ctx, Op{Entry: Entry{ID: r.ID, Category: Category(r.Category)}, ExpectedVersion: w.ExpectedVersion})
		if errors.Is(err, store.ErrConflict) && w.Actor == ActorExtraction {
			conflict = true
		} else if err != nil {
			return Result{}, err
		}
	}
	if w.Actor != ActorUser {
		code, detail, err := s.checkReplay(ctx, w, times)
		if err != nil {
			return Result{}, err
		}
		if code != "" {
			return s.reject(ctx, w, code, detail, false)
		}
	}
	category, scope := w.Category, w.Scope
	var old *Scope
	if row != nil {
		o := memoryRowScope(*row)
		old, category = &o, Category(row.Category)
		if scope.Kind == "" {
			scope = o
		}
	}
	if !conflict && takesEffect(w, category, old, scope) {
		return s.applyNow(ctx, w, row, category, scope)
	}
	return s.propose(ctx, w, row, category, scope)
}

func (w Write) checkShape() error {
	switch w.Actor {
	case ActorUser:
		if len(w.Evidence) > 0 {
			return invalid("a user's write carries no evidence")
		}
	case ActorCompanion:
		if w.Session == "" || w.Batch != nil {
			return invalid("a companion's write needs its session and no batch")
		}
	case ActorExtraction:
		if w.Batch == nil || w.Batch.Session == "" || w.Batch.From > w.Batch.To || w.Session != "" {
			return invalid("an extraction's write needs a valid batch and no session")
		}
	default:
		return invalid("actor %q cannot write", w.Actor)
	}
	for _, ev := range w.Evidence {
		if !ev.Kind.Valid() {
			return invalid("evidence kind %q", ev.Kind)
		}
	}
	if w.Scope.Kind != "" && !validScope(w.Scope) {
		return invalid("scope %q with value %q", w.Scope.Kind, w.Scope.Value)
	}
	switch w.Action {
	case ActionCreate:
		if w.Target != "" || w.ExpectedVersion != 0 || !w.Category.Valid() || w.Scope.Kind == "" || w.Body == "" {
			return invalid("create needs a category, a scope and a body, and no target")
		}
	case ActionUpdate:
		if w.Target == "" || w.ExpectedVersion < 1 || w.Body == "" {
			return invalid("update needs a target, its version and a body")
		}
	case ActionWithdraw:
		if w.Target == "" || w.ExpectedVersion < 1 || w.Body != "" || w.Scope.Kind != "" {
			return invalid("withdraw needs a target and its version, and no body or scope")
		}
		if w.Actor != ActorUser && w.Reason == "" {
			return invalid("withdraw needs a reason")
		}
	default:
		return invalid("action %q", w.Action)
	}
	return nil
}

func memoryRowScope(row store.MemoryRow) Scope {
	return Scope{Kind: ScopeKind(row.ScopeKind), Value: row.ScopeValue}
}

func validScope(sc Scope) bool {
	return sc.Kind.Valid() && (sc.Kind == ScopeGlobal) == (sc.Value == "")
}

func (w Write) checkTarget(r store.MemoryRow) error {
	if w.Category != "" && string(w.Category) != r.Category {
		return invalid("entry %s cannot move from category %s", r.ID, r.Category)
	}
	switch Status(r.Status) {
	case StatusActive, StatusStale:
		return nil
	case StatusWithdrawn:
		if w.Action == ActionUpdate {
			return nil
		}
	}
	return invalid("entry %s is %s", r.ID, r.Status)
}

func (s *Service) applyNow(ctx context.Context, w Write, row *store.MemoryRow, category Category, scope Scope) (Result, error) {
	var e Entry
	if row == nil {
		id, err := NewID()
		if err != nil {
			return Result{}, err
		}
		e = Entry{ID: id, Category: category}
	} else {
		var err error
		if e, err = s.read(*row); err != nil {
			return Result{}, err
		}
	}
	if w.Action == ActionWithdraw {
		e.Status = StatusWithdrawn
	} else {
		e.Status, e.Scope, e.Evidence, e.Body = StatusActive, scope, w.Evidence, w.Body
	}
	e.ReplacedBy = ""
	saved, err := s.commit(ctx, Op{ID: w.OpID, Entry: e, ExpectedVersion: w.ExpectedVersion, Actor: w.Actor, Reason: w.Reason})
	if err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeApplied, Entry: saved, Reason: w.Reason}, nil
}

func (s *Service) propose(ctx context.Context, w Write, row *store.MemoryRow, category Category, scope Scope) (Result, error) {
	id, err := NewID()
	if err != nil {
		return Result{}, err
	}
	body := w.Body
	if w.Action == ActionWithdraw {
		body = w.Reason
	}
	e := Entry{ID: id, Category: category, Status: StatusCandidate, Scope: scope, Evidence: w.Evidence, Body: body}
	saved, err := s.commit(ctx, Op{ID: w.OpID, Entry: e, Actor: w.Actor, Reason: w.Reason})
	if err != nil {
		return Result{}, err
	}
	version := 0
	if row != nil {
		version = row.Version // a conflicting extraction is reviewed against the entry as it stands
	}
	if err := s.addCandidate(ctx, w, id, version); err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeCandidate, Entry: saved, Reason: w.Reason}, nil
}

func (s *Service) addCandidate(ctx context.Context, w Write, id string, version int) error {
	return s.store.AddMemoryCandidate(ctx, store.MemoryCandidate{
		EntryID: id, Action: string(w.Action), TargetID: w.Target, ExpectedVersion: version, Actor: string(w.Actor), Reason: w.Reason,
	})
}

func (s *Service) reject(ctx context.Context, w Write, code, detail string, secret bool) (Result, error) {
	content, hash := w.Body, hashOf([]byte(w.Body))
	if secret {
		content, hash = "", ""
	}
	err := s.store.RecordMemoryRejection(ctx, store.MemoryOp{
		ID: w.OpID, EntryID: w.Target, ExpectedVersion: w.ExpectedVersion, Actor: string(w.Actor),
		Reason: code + ": " + detail, Content: content, Hash: hash,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeRejected, Code: code, Reason: detail}, nil
}

// earlierResult restores the result of an OpID already recorded.
func (s *Service) earlierResult(ctx context.Context, w Write) (Result, bool, error) {
	prev, err := s.store.MemoryOpByID(ctx, w.OpID)
	if errors.Is(err, store.ErrNotFound) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, err
	}
	switch prev.State {
	case store.MemoryOpRejected:
		code, detail, _ := strings.Cut(prev.Reason, ": ")
		return Result{Outcome: OutcomeRejected, Code: code, Reason: detail}, true, nil
	case store.MemoryOpCommitted:
	default:
		return Result{}, true, fmt.Errorf("%w: memory op %s is %s", store.ErrConflict, w.OpID, prev.State)
	}
	e, err := Decode([]byte(prev.Content))
	if err != nil {
		return Result{}, true, fmt.Errorf("memory: op %s: %w", w.OpID, err)
	}
	if e.Status != StatusCandidate {
		return Result{Outcome: OutcomeApplied, Entry: e, Reason: prev.Reason}, true, nil
	}
	// A crash can fall between the entry's commit and its candidate row.
	if err := s.restoreCandidate(ctx, w, e.ID); err != nil {
		return Result{}, true, err
	}
	return Result{Outcome: OutcomeCandidate, Entry: e, Reason: prev.Reason}, true, nil
}

func (s *Service) restoreCandidate(ctx context.Context, w Write, id string) error {
	version := 0
	if w.Target != "" {
		row, err := s.store.MemoryIndexEntry(ctx, w.Target)
		if err != nil {
			return err
		}
		version = row.Version
	}
	if err := s.addCandidate(ctx, w, id, version); err != nil && !errors.Is(err, store.ErrExists) {
		return err
	}
	return nil
}

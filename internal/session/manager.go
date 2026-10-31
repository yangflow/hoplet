// Package session runs Hoplet sessions: it creates them in the store,
// starts each harness (behind a shim, or directly for harnesses with their
// own server), ingests the harness's events into the store (committing the
// shim's spool offset in the same transaction), and reattaches to running
// shims after the daemon restarts.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// Errors.
var (
	// ErrBusy is returned by Send while the session's turn is running or a
	// message is on its way to the harness.
	ErrBusy = errors.New("session: a turn is running")
	// ErrNotRunning is returned for operations that need a live harness.
	ErrNotRunning = errors.New("session: harness is not running")
	// ErrUnknownHarness is returned for a harness kind the manager lacks.
	ErrUnknownHarness = errors.New("session: unknown harness")
	// ErrUnsupportedVersion is returned when a harness is outside its
	// verified version range and so must not be started.
	ErrUnsupportedVersion = errors.New("session: unsupported harness version")
	// ErrInvalidOption is returned by Respond for an option the approval does
	// not offer.
	ErrInvalidOption = errors.New("session: not an option of the approval")

	// errNoShim says nothing listens on the session's shim socket.
	errNoShim = errors.New("session: no shim is running")
)

const (
	// readyTimeout bounds how long a freshly spawned shim may take to accept
	// connections.
	readyTimeout = 10 * time.Second
	// stopWait bounds each of the two waits in Stop.
	stopWait = 5 * time.Second
)

// Config configures a Manager.
type Config struct {
	Store *store.Store
	// Root is the parent of the per-session directories.
	Root string
	// Harnesses are the available harnesses by kind. A StdioHarness runs
	// behind a shim; a DirectHarness is started and resumed directly.
	Harnesses map[string]harness.Harness
	Spawner   Spawner
	// Env is the complete, already-whitelisted environment of harness
	// processes (login-shell environment, proxy variables, MCP token), built
	// by the daemon. The session package never inherits the daemon's own
	// environment.
	Env    []string
	Logger *slog.Logger
}

// Manager runs sessions. Its methods are safe for concurrent use.
type Manager struct {
	cfg Config
	log *slog.Logger
	// ctx scopes background work (ingestion, reattaching); it outlives
	// the requests that start it and ends with the manager.
	ctx    context.Context
	cancel context.CancelFunc
	// closeCh is closed when Close begins, to wake sleeping reattachers.
	closeCh chan struct{}

	// appendFn appends the events ingestion produces; it is Store.Append
	// unless a test replaces it.
	appendFn func(context.Context, store.AppendRequest) (store.AppendResult, error)
	// ready bounds how long a spawned shim may take to accept connections.
	ready time.Duration

	mu      sync.Mutex
	live    map[proto.SessionID]*live
	locks   map[proto.SessionID]*sync.Mutex
	closing bool
	wg      sync.WaitGroup
}

// live is a session whose harness the manager is attached to.
type live struct {
	id    proto.SessionID
	h     harness.Harness
	stdio *ShimStdio // nil for a direct harness
	// acker acknowledges the exit record to a shim; nil for a direct harness.
	acker interface{ Ack(int64) error }
	run   harness.Run
	done  chan struct{} // closed when ingestion stops

	sendMu sync.Mutex
	// pending is true from a message being sent until its turn is recorded
	// started (or the send failed, or the harness exited).
	pending atomic.Bool
	// stopping is set by Stop: the harness is expected to exit.
	stopping atomic.Bool

	// Owned by the ingest goroutine.
	native string // the last native session ID written to the store
	note   string // an Error to record with the next successful append
}

// New returns a manager; call Recover to reattach to running shims.
func New(cfg Config) (*Manager, error) {
	if cfg.Store == nil || cfg.Root == "" || cfg.Spawner == nil {
		return nil, errors.New("session: store, root and spawner are required")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg: cfg, log: log, ctx: ctx, cancel: cancel, closeCh: make(chan struct{}),
		appendFn: cfg.Store.Append, ready: readyTimeout,
		live: map[proto.SessionID]*live{}, locks: map[proto.SessionID]*sync.Mutex{},
	}, nil
}

// CreateRequest describes a new session.
type CreateRequest struct {
	Mode       proto.Mode
	Harness    string
	Directory  string // working directory; required for code sessions
	Isolation  proto.Isolation
	Permission proto.PermissionMode
	Title      string
	Origin     proto.Origin
}

// Create stores a new session. The harness starts on the first Send.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (proto.Session, error) {
	h, ok := m.cfg.Harnesses[req.Harness]
	if !ok {
		return proto.Session{}, fmt.Errorf("%w: %q", ErrUnknownHarness, req.Harness)
	}
	info, err := h.Probe(ctx)
	if err != nil {
		return proto.Session{}, fmt.Errorf("session: probe %s: %w", req.Harness, err)
	}
	if !info.Supported {
		return proto.Session{}, fmt.Errorf("%w: %s %s", ErrUnsupportedVersion, req.Harness, info.Version)
	}
	if req.Mode == proto.ModeCode && req.Directory == "" {
		return proto.Session{}, errors.New("session: a code session needs a directory")
	}
	if req.Mode == proto.ModeCode && req.Isolation == "" {
		req.Isolation = proto.InPlace
	}
	if req.Origin == "" {
		req.Origin = proto.OriginMe
	}
	id, err := newID()
	if err != nil {
		return proto.Session{}, err
	}
	// The directory comes first so a stored session always has one.
	if err := os.MkdirAll(m.dir(id), 0o700); err != nil {
		return proto.Session{}, fmt.Errorf("session: create session dir: %w", err)
	}
	sess, err := m.cfg.Store.CreateSession(ctx, proto.Session{
		ID: id, Mode: req.Mode, Harness: proto.HarnessRef{Kind: req.Harness, Version: info.Version},
		Directory: req.Directory, Isolation: req.Isolation, Permission: req.Permission,
		Owner: proto.OwnerMe, Origin: req.Origin, Title: req.Title,
	})
	if err != nil {
		return proto.Session{}, errors.Join(err, os.RemoveAll(m.dir(id)))
	}
	return sess, nil
}

func newID() (proto.SessionID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: new id: %w", err)
	}
	return proto.SessionID(hex.EncodeToString(b[:])), nil
}

func (m *Manager) dir(id proto.SessionID) string { return filepath.Join(m.cfg.Root, string(id)) }

// lockSession serializes lifecycle work (attach, spawn, reattach, stop) on
// one session, so only one connection to its shim is made at a time.
func (m *Manager) lockSession(id proto.SessionID) func() {
	m.mu.Lock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) isClosing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closing
}

// SendResult says where Send recorded the user's message.
type SendResult struct {
	Seq  uint64 // the message event's sequence number
	Turn uint32 // the turn the message starts
}

// Send records the user's message and sends it to the harness, starting the
// harness if needed. key makes the call idempotent: a retry with the same
// key returns the original result, and resends the message only if it never
// reached the harness (its turn has not started and nothing is in flight).
func (m *Manager) Send(ctx context.Context, id proto.SessionID, text, key string) (SendResult, error) {
	if key == "" {
		return SendResult{}, errors.New("session: send needs an idempotency key")
	}
	l, err := m.ensure(ctx, id)
	if err != nil {
		return SendResult{}, err
	}
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	last, open, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil {
		return SendResult{}, err
	}
	rcpt, found, err := m.cfg.Store.Receipt(ctx, id, "send:"+key)
	if err != nil {
		return SendResult{}, err
	}
	if found {
		return m.resend(ctx, l, text, rcpt, last, open)
	}
	if l.pending.Load() || open {
		return SendResult{}, ErrBusy
	}
	res, err := m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: id, Turn: last + 1, ReceiptKey: "send:" + key,
		Entries: []store.Entry{{Payload: proto.Message{ID: key, Role: proto.RoleUser, Text: text}}},
	})
	if err != nil {
		return SendResult{}, err
	}
	out := SendResult{Seq: res.FirstSeq, Turn: last + 1}
	return out, m.deliver(ctx, l, text, out)
}

// resend handles a retry of a recorded message.
func (m *Manager) resend(ctx context.Context, l *live, text string, rcpt store.AppendResult, last uint32, open bool) (SendResult, error) {
	evs, err := m.cfg.Store.Events(ctx, l.id, rcpt.FirstSeq-1, 1)
	if err != nil {
		return SendResult{}, err
	}
	if len(evs) == 0 || evs[0].Seq != rcpt.FirstSeq {
		return SendResult{}, fmt.Errorf("session: message event %d of %s is missing", rcpt.FirstSeq, l.id)
	}
	out := SendResult{Seq: rcpt.FirstSeq, Turn: evs[0].Turn}
	if last >= out.Turn || l.pending.Load() || open {
		return out, nil // the turn started, or the message is on its way
	}
	return out, m.deliver(ctx, l, text, out)
}

// deliver sends a recorded message to the harness. On failure it records the
// failure in the message's turn; a retry with the same key resends, because
// the turn never started.
func (m *Manager) deliver(ctx context.Context, l *live, text string, msg SendResult) error {
	// pending is set before sending: a fast harness may start its turn
	// before Send returns, and ingestion clears pending when that is recorded.
	l.pending.Store(true)
	err := l.run.Send(ctx, text)
	if err == nil {
		return nil
	}
	l.pending.Store(false)
	_, aerr := m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: l.id, Turn: msg.Turn,
		Entries: []store.Entry{{Payload: proto.Error{Message: "send failed: " + err.Error()}}},
	})
	if aerr != nil {
		m.log.Error("record send failure", "session", l.id, "err", aerr)
	}
	return err
}

// Respond answers an approval with one of its options. The first answer wins;
// later ones get store.ErrConflict. A retry with the same key records
// nothing again and delivers the decision again, which the harness ignores if
// it already has it; ErrNotRunning means the decision is recorded but no live
// harness received it, so the caller retries once the session is running.
func (m *Manager) Respond(ctx context.Context, id proto.SessionID, approvalID, optionID, decidedBy, key string) error {
	pending, err := m.cfg.Store.PendingApprovals(ctx, id)
	if err != nil {
		return err
	}
	outcome := proto.OutcomeAllowed
	var turn uint32
	found := false
	for _, p := range pending {
		if p.Request.ID != approvalID {
			continue
		}
		found = true
		valid := false
		for _, o := range p.Request.Options {
			if o.ID == optionID {
				valid = true
				if o.Scope == proto.ScopeDeny {
					outcome = proto.OutcomeDenied
				}
			}
		}
		if !valid {
			return fmt.Errorf("%w: %q for approval %q", ErrInvalidOption, optionID, approvalID)
		}
		evs, err := m.cfg.Store.Events(ctx, id, p.RequestSeq-1, 1)
		if err != nil {
			return err
		}
		if len(evs) == 0 || evs[0].Seq != p.RequestSeq {
			return fmt.Errorf("session: request event %d of %s is missing", p.RequestSeq, id)
		}
		turn = evs[0].Turn
	}
	if !found {
		// Already decided, unknown, or a retry: ResolveApproval tells which.
		if turn, _, err = m.cfg.Store.TurnState(ctx, id); err != nil {
			return err
		}
	}
	if _, err := m.cfg.Store.ResolveApproval(ctx, id, turn, time.Now(), proto.ApprovalResolved{
		ID: approvalID, Outcome: outcome, OptionID: optionID, DecidedBy: decidedBy,
	}, key); err != nil {
		return err
	}
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	return l.run.Respond(ctx, approvalID, optionID)
}

// Interrupt stops the running turn.
func (m *Manager) Interrupt(ctx context.Context, id proto.SessionID) error {
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	return l.run.Interrupt(ctx)
}

// Steer sends text to the harness during a running turn.
func (m *Manager) Steer(ctx context.Context, id proto.SessionID, text string) error {
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	return l.run.Steer(ctx, text)
}

// Answer answers a question the harness asked.
func (m *Manager) Answer(ctx context.Context, id proto.SessionID, questionID, answer string) error {
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	return l.run.Answer(ctx, questionID, answer)
}

// SetPermission changes the session's permission mode. It stores the mode
// (the harness starts with it next time) and applies it to a running
// harness. It refuses, writing nothing, when the harness cannot change mode.
func (m *Manager) SetPermission(ctx context.Context, id proto.SessionID, mode proto.PermissionMode) error {
	sess, err := m.cfg.Store.Session(ctx, id)
	if err != nil {
		return err
	}
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	info, err := h.Probe(ctx)
	if err != nil {
		return fmt.Errorf("session: probe %s: %w", sess.Harness.Kind, err)
	}
	if !info.Capabilities.SetMode {
		return harness.ErrUnsupported
	}
	if _, err := m.cfg.Store.UpdateSession(ctx, id, store.SessionUpdate{Permission: &mode}); err != nil {
		return err
	}
	l, err := m.attached(id)
	if err != nil {
		return nil // not running: the stored mode applies at the next start
	}
	return l.run.SetMode(ctx, mode)
}

func (m *Manager) attached(id proto.SessionID) (*live, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.live[id]
	if !ok {
		return nil, ErrNotRunning
	}
	return l, nil
}

// ensure returns the live session, attaching to its shim or starting one.
func (m *Manager) ensure(ctx context.Context, id proto.SessionID) (*live, error) {
	if l, err := m.attached(id); err == nil {
		return l, nil
	}
	unlock := m.lockSession(id)
	defer unlock()
	if m.isClosing() {
		return nil, errors.New("session: manager is closing")
	}
	if l, err := m.attached(id); err == nil {
		return l, nil
	}
	sess, err := m.cfg.Store.Session(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess.ArchivedAt != nil {
		return nil, fmt.Errorf("session: %s is archived", id)
	}
	l, err := m.attach(ctx, sess, false, "")
	if err == nil {
		return l, nil
	}
	if !errors.Is(err, errNoShim) {
		m.recordAttachError(ctx, id, err)
		return nil, err
	}
	if err := m.spawn(ctx, sess); err != nil {
		return nil, err
	}
	l, err = m.attach(ctx, sess, true, "")
	if err != nil {
		err = fmt.Errorf("%w%s", err, shimLogTail(m.dir(id)))
		m.recordAttachError(ctx, id, err)
		return nil, err
	}
	return l, nil
}

func (m *Manager) specFor(sess proto.Session, inTurn bool) harness.Spec {
	return harness.Spec{
		Session: sess.ID, WorkDir: m.workDir(sess), Env: m.cfg.Env, Mode: sess.Permission,
		Resume: sess.Harness.NativeID,
		State:  harness.State{InTurn: inTurn, Native: sess.Harness.NativeID},
	}
}

func (m *Manager) workDir(sess proto.Session) string {
	if sess.Directory != "" {
		return sess.Directory
	}
	return m.dir(sess.ID) // personal sessions work in their own directory
}

// startSpec is the spec for starting a harness: the resume ID is dropped when
// the harness cannot resume.
func (m *Manager) startSpec(ctx context.Context, h harness.Harness, sess proto.Session) (harness.Spec, error) {
	info, err := h.Probe(ctx)
	if err != nil {
		return harness.Spec{}, fmt.Errorf("session: probe %s: %w", sess.Harness.Kind, err)
	}
	if !info.Supported {
		return harness.Spec{}, fmt.Errorf("%w: %s %s", ErrUnsupportedVersion, sess.Harness.Kind, info.Version)
	}
	spec := m.specFor(sess, false)
	if !info.Capabilities.Resume {
		spec.Resume = "" // the harness can't resume; start fresh
	}
	return spec, nil
}

func (m *Manager) spawn(ctx context.Context, sess proto.Session) error {
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	sh, ok := h.(harness.StdioHarness)
	if !ok {
		return fmt.Errorf("session: harness %q does not run behind a shim", sess.Harness.Kind)
	}
	spec, err := m.startSpec(ctx, h, sess)
	if err != nil {
		return err
	}
	l, err := sh.Launch(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir(sess.ID), 0o700); err != nil {
		return fmt.Errorf("session: create session dir: %w", err)
	}
	return m.cfg.Spawner.Spawn(ctx, m.dir(sess.ID), spec.WorkDir, l)
}

// attach connects to the session's harness and starts ingesting; the caller
// holds the session lock. For a shim it resumes from the committed offset;
// with wait it retries until a just-spawned shim is ready, without it fails
// with errNoShim at once if no shim is listening. A direct harness is
// started (resumed). note is an Error to record with the first append.
func (m *Manager) attach(ctx context.Context, sess proto.Session, wait bool, note string) (*live, error) {
	if l, err := m.attached(sess.ID); err == nil {
		return l, nil
	}
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	off, err := m.cfg.Store.IngestOffset(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	last, open, err := m.cfg.Store.TurnState(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	l := &live{id: sess.ID, h: h, done: make(chan struct{}), native: sess.Harness.NativeID, note: note}
	switch hh := h.(type) {
	case harness.StdioHarness:
		var c *shim.Client
		if wait {
			dctx, cancel := context.WithTimeout(ctx, m.ready)
			c, err = dialWhenReady(dctx, m.dir(sess.ID), off)
			cancel()
		} else {
			c, err = shim.Dial(ctx, m.dir(sess.ID), off)
			if err != nil && noListener(err) {
				err = fmt.Errorf("%w: %w", errNoShim, err)
			}
		}
		if err != nil {
			return nil, err
		}
		l.stdio = NewShimStdio(c)
		l.acker = l.stdio
		spec := m.specFor(sess, open)
		spec.Resume = "" // attaching to a running harness resumes nothing
		l.run, err = hh.Attach(ctx, spec, l.stdio)
		if err != nil {
			return nil, errors.Join(err, l.stdio.Close())
		}
	case harness.DirectHarness:
		spec, err := m.startSpec(ctx, h, sess)
		if err != nil {
			return nil, err
		}
		spec.State.InTurn = open
		// The run outlives the request, so it gets the manager's context.
		if l.run, err = hh.Start(m.ctx, spec); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("session: harness %q is neither stdio nor direct", sess.Harness.Kind)
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, errors.Join(errors.New("session: manager is closing"), l.run.Close())
	}
	if existing, ok := m.live[sess.ID]; ok {
		m.mu.Unlock()
		return existing, l.run.Close()
	}
	m.live[sess.ID] = l
	m.wg.Add(1)
	m.mu.Unlock()
	go m.ingest(l, last, open)
	return l, nil
}

// noListener reports whether a dial failed because nothing listens: the
// socket is missing or refuses connections.
func noListener(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

// recordAttachError notes in the session's log that the harness could not be
// reached. The turn stays open: the harness may well still be running.
func (m *Manager) recordAttachError(ctx context.Context, id proto.SessionID, cause error) {
	msg := "cannot attach to the harness: " + cause.Error()
	var serr *shim.Error
	if errors.As(cause, &serr) && serr.Code == shim.CodeBadOffset {
		msg += " (the shim's spool is shorter than what was already ingested; archive this session)"
	}
	m.log.Error("cannot attach to the harness", "session", id, "err", cause)
	if _, err := m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: id, Entries: []store.Entry{{Payload: proto.Error{Message: msg}}},
	}); err != nil {
		m.log.Error("record attach failure", "session", id, "err", err)
	}
}

// Recover reattaches to every running shim and closes turns whose harness
// is gone. Call it once at daemon start, before serving commands. Direct
// harnesses with an open turn are started again; idle ones start on Send.
// Archived sessions whose shim still runs are stopped.
func (m *Manager) Recover(ctx context.Context) error {
	sessions, err := m.cfg.Store.Sessions(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sess := range sessions {
		if err := m.recoverOne(ctx, sess); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) recoverOne(ctx context.Context, sess proto.Session) error {
	unlock := m.lockSession(sess.ID)
	defer unlock()
	h := m.cfg.Harnesses[sess.Harness.Kind]
	_, isStdio := h.(harness.StdioHarness)
	direct := h != nil && !isStdio
	if direct {
		if sess.ArchivedAt != nil {
			return nil
		}
		if _, open, err := m.cfg.Store.TurnState(ctx, sess.ID); err != nil || !open {
			return err
		}
	}
	l, err := m.attach(ctx, sess, false, "")
	switch {
	case err == nil:
		if sess.ArchivedAt != nil {
			return m.stopLive(ctx, l)
		}
		return nil
	case sess.ArchivedAt != nil:
		return nil
	case errors.Is(err, errNoShim) || direct:
		return m.abortOpenTurn(ctx, sess.ID, "the harness was not running when the daemon started")
	default:
		m.recordAttachError(ctx, sess.ID, err)
		return nil
	}
}

// abortOpenTurn records TurnEnded{aborted} if the store still has an open
// turn; the store is the source of truth for turn state.
func (m *Manager) abortOpenTurn(ctx context.Context, id proto.SessionID, why string) error {
	last, open, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil || !open {
		return err
	}
	_, err = m.appendFn(ctx, store.AppendRequest{
		Session: id, Turn: last,
		Entries: []store.Entry{{Payload: proto.TurnEnded{Reason: proto.TurnAborted, Message: why}}},
	})
	return err
}

// Stop ends the session's harness: TERM, then KILL if it does not exit in
// time. It returns once the exit is recorded and acknowledged, and does
// nothing when the harness is not running.
func (m *Manager) Stop(ctx context.Context, id proto.SessionID) error {
	unlock := m.lockSession(id)
	defer unlock()
	l, err := m.attached(id)
	if err != nil {
		return nil
	}
	return m.stopLive(ctx, l)
}

func (m *Manager) stopLive(ctx context.Context, l *live) error {
	l.stopping.Store(true)
	if l.stdio == nil {
		// A direct harness has no signal to send; closing the run ends it.
		err := l.run.Close()
		if werr := waitDone(ctx, l.done, stopWait); werr != nil {
			return errors.Join(err, werr)
		}
		return errors.Join(err, m.abortOpenTurn(ctx, l.id, "the session was stopped"))
	}
	if err := l.stdio.Signal("TERM"); err != nil {
		return fmt.Errorf("session: stop %s: %w", l.id, err)
	}
	if err := waitDone(ctx, l.done, stopWait); err == nil {
		return nil
	} else if ctx.Err() != nil {
		return err
	}
	if err := l.stdio.Signal("KILL"); err != nil {
		return fmt.Errorf("session: kill %s: %w", l.id, err)
	}
	return waitDone(ctx, l.done, stopWait)
}

func waitDone(ctx context.Context, done <-chan struct{}, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return nil
	case <-t.C:
		return errors.New("session: the harness did not exit in time")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Archive stops the session's harness and marks the session archived.
func (m *Manager) Archive(ctx context.Context, id proto.SessionID) error {
	if err := m.Stop(ctx, id); err != nil {
		return err
	}
	return m.cfg.Store.ArchiveSession(ctx, id, time.Now())
}

// Close detaches from every shim without stopping it, as a daemon that
// exits does; running turns continue in their shims. Direct harnesses end
// with their owner.
func (m *Manager) Close() error {
	m.mu.Lock()
	first := !m.closing
	m.closing = true
	lives := make([]*live, 0, len(m.live))
	for _, l := range m.live {
		lives = append(lives, l)
	}
	m.mu.Unlock()
	if first {
		close(m.closeCh)
	}
	var errs []error
	for _, l := range lives {
		errs = append(errs, l.run.Close())
	}
	m.wg.Wait()
	m.cancel()
	return errors.Join(errs...)
}

// startReattach schedules reattach under the manager's wait group, unless the
// manager is closing.
func (m *Manager) startReattach(id proto.SessionID, delay time.Duration, note string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return
	}
	m.wg.Add(1)
	go m.reattach(id, delay, note)
}

const (
	reattachAttempts = 8
	reattachBackoff  = 100 * time.Millisecond
	reattachMax      = 5 * time.Second
)

// reattach reconnects to a session whose connection ended or whose ingestion
// failed. Failures other than "no shim" are retried with backoff, since the
// shim is probably still there.
func (m *Manager) reattach(id proto.SessionID, delay time.Duration, note string) {
	defer m.wg.Done()
	if !m.sleep(delay) {
		return
	}
	backoff := reattachBackoff
	var err error
	for attempt := 0; attempt < reattachAttempts; attempt++ {
		if attempt > 0 && !m.sleep(backoff) {
			return
		}
		backoff = min(2*backoff, reattachMax)
		if err = m.tryReattach(id, note); err == nil {
			return
		}
		if errors.Is(err, errNoShim) {
			if !m.isClosing() {
				if aerr := m.abortOpenTurn(m.ctx, id, "lost the connection to the harness"); aerr != nil {
					m.log.Error("abort open turn", "session", id, "err", aerr)
				}
			}
			return
		}
		m.log.Warn("reattach failed", "session", id, "attempt", attempt+1, "err", err)
	}
	if !m.isClosing() {
		m.recordAttachError(m.ctx, id, err)
	}
}

func (m *Manager) tryReattach(id proto.SessionID, note string) error {
	unlock := m.lockSession(id)
	defer unlock()
	if m.isClosing() {
		return nil
	}
	sess, err := m.cfg.Store.Session(m.ctx, id)
	if err != nil {
		return err
	}
	_, err = m.attach(m.ctx, sess, false, note)
	return err
}

// sleep waits d and reports false if the manager began closing meanwhile.
func (m *Manager) sleep(d time.Duration) bool {
	if d <= 0 {
		return !m.isClosing()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return !m.isClosing()
	case <-m.closeCh:
		return false
	}
}

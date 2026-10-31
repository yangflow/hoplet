// Package session runs Hoplet sessions: it creates them in the store,
// starts each harness behind a shim, ingests the harness's events into the
// store (committing the shim's spool offset in the same transaction), and
// reattaches to running shims after the daemon restarts.
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
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// Errors.
var (
	// ErrBusy is returned by Send while the session's turn is running.
	ErrBusy = errors.New("session: a turn is running")
	// ErrNotRunning is returned for operations that need a live harness.
	ErrNotRunning = errors.New("session: harness is not running")
	// ErrUnknownHarness is returned for a harness kind the manager lacks.
	ErrUnknownHarness = errors.New("session: unknown harness")
)

// readyTimeout bounds how long a freshly spawned shim may take to accept
// connections.
const readyTimeout = 10 * time.Second

// Config configures a Manager.
type Config struct {
	Store     *store.Store
	Root      string // parent of the per-session directories
	Harnesses map[string]harness.StdioHarness
	Spawner   Spawner
	Env       []string // base environment for harnesses
	Logger    *slog.Logger
	// OnAppend, when set, is called after events are committed for a session.
	OnAppend func(proto.SessionID)
}

// Manager runs sessions. Its methods are safe for concurrent use.
type Manager struct {
	cfg Config
	log *slog.Logger
	// ctx scopes background work (ingestion, reattaching); it outlives
	// the requests that start it and ends with the manager.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	live    map[proto.SessionID]*live
	closing bool
	wg      sync.WaitGroup
}

// live is a session whose harness the manager is attached to.
type live struct {
	id    proto.SessionID
	h     harness.StdioHarness
	stdio *ShimStdio
	run   harness.Run
	done  chan struct{} // closed when ingestion stops
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
	return &Manager{cfg: cfg, log: log, ctx: ctx, cancel: cancel, live: map[proto.SessionID]*live{}}, nil
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
		return proto.Session{}, fmt.Errorf("session: %s %s is outside the supported versions", req.Harness, info.Version)
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
	sess, err := m.cfg.Store.CreateSession(ctx, proto.Session{
		ID: id, Mode: req.Mode, Harness: proto.HarnessRef{Kind: req.Harness, Version: info.Version},
		Directory: req.Directory, Isolation: req.Isolation, Permission: req.Permission,
		Owner: proto.OwnerMe, Origin: req.Origin, Title: req.Title,
	})
	if err != nil {
		return proto.Session{}, err
	}
	if err := os.MkdirAll(m.dir(id), 0o700); err != nil {
		return proto.Session{}, fmt.Errorf("session: create session dir: %w", err)
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

// Send records the user's message and sends it to the harness, starting the
// harness if needed. key makes the call idempotent: a retry with the same
// key does not record or send the message again once its turn has started.
func (m *Manager) Send(ctx context.Context, id proto.SessionID, text, key string) error {
	if key == "" {
		return errors.New("session: send needs an idempotency key")
	}
	l, err := m.ensure(ctx, id)
	if err != nil {
		return err
	}
	last, open, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil {
		return err
	}
	if open {
		return ErrBusy
	}
	res, err := m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: id, Turn: last + 1, ReceiptKey: "send:" + key,
		Entries: []store.Entry{{Payload: proto.Message{ID: key, Role: proto.RoleUser, Text: text}}},
	})
	if err != nil {
		return err
	}
	m.notify(id)
	if res.Duplicate {
		// The message is recorded; it was sent unless the daemon died in
		// between, which shows as its turn never having started.
		return nil
	}
	return l.run.Send(ctx, text)
}

// Respond answers an approval with one of the harness's options. The first
// answer wins; later ones get store.ErrConflict.
func (m *Manager) Respond(ctx context.Context, id proto.SessionID, approvalID, optionID, decidedBy, key string) error {
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	pending, err := m.cfg.Store.PendingApprovals(ctx, id)
	if err != nil {
		return err
	}
	outcome := proto.OutcomeAllowed
	found := false
	for _, p := range pending {
		if p.Request.ID != approvalID {
			continue
		}
		found = true
		for _, o := range p.Request.Options {
			if o.ID == optionID && o.Scope == proto.ScopeDeny {
				outcome = proto.OutcomeDenied
			}
		}
	}
	last, _, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil {
		return err
	}
	res, err := m.cfg.Store.ResolveApproval(ctx, id, last, time.Now(), proto.ApprovalResolved{
		ID: approvalID, Outcome: outcome, OptionID: optionID, DecidedBy: decidedBy,
	}, key)
	if err != nil {
		return err
	}
	m.notify(id)
	if res.Duplicate || !found {
		return nil
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

func (m *Manager) attached(id proto.SessionID) (*live, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.live[id]
	if !ok {
		return nil, ErrNotRunning
	}
	return l, nil
}

func (m *Manager) notify(id proto.SessionID) {
	if m.cfg.OnAppend != nil {
		m.cfg.OnAppend(id)
	}
}

// ensure returns the live session, attaching to its shim or spawning one.
func (m *Manager) ensure(ctx context.Context, id proto.SessionID) (*live, error) {
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
	if l, err := m.attach(ctx, sess, false); err == nil {
		return l, nil
	}
	if err := m.spawn(ctx, sess); err != nil {
		return nil, err
	}
	return m.attach(ctx, sess, true)
}

func (m *Manager) spec(sess proto.Session, inTurn bool) harness.Spec {
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

func (m *Manager) spawn(ctx context.Context, sess proto.Session) error {
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	spec := m.spec(sess, false)
	if spec.Resume != "" {
		if info, err := h.Probe(ctx); err != nil || !info.Capabilities.Resume {
			spec.Resume = "" // the harness can't resume; start fresh
		}
	}
	l, err := h.Launch(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir(sess.ID), 0o700); err != nil {
		return fmt.Errorf("session: create session dir: %w", err)
	}
	return m.cfg.Spawner.Spawn(ctx, m.dir(sess.ID), spec.WorkDir, l)
}

// attach connects to the session's shim from the committed offset and
// starts ingesting. With wait it retries until a just-spawned shim is ready;
// without, it fails at once if no shim is listening.
func (m *Manager) attach(ctx context.Context, sess proto.Session, wait bool) (*live, error) {
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	off, err := m.cfg.Store.IngestOffset(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	var c *shim.Client
	if wait {
		dctx, cancel := context.WithTimeout(ctx, readyTimeout)
		c, err = dialWhenReady(dctx, m.dir(sess.ID), off)
		cancel()
	} else {
		c, err = shim.Dial(ctx, m.dir(sess.ID), off)
	}
	if err != nil {
		return nil, err
	}
	last, open, err := m.cfg.Store.TurnState(ctx, sess.ID)
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	stdio := NewShimStdio(c)
	run, err := h.Attach(ctx, m.spec(sess, open), stdio)
	if err != nil {
		return nil, errors.Join(err, stdio.Close())
	}
	l := &live{id: sess.ID, h: h, stdio: stdio, run: run, done: make(chan struct{})}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, errors.Join(errors.New("session: manager is closing"), run.Close())
	}
	if existing, ok := m.live[sess.ID]; ok {
		m.mu.Unlock()
		return existing, run.Close()
	}
	m.live[sess.ID] = l
	m.wg.Add(1)
	m.mu.Unlock()
	go m.ingest(m.ctx, l, last, open)
	return l, nil
}

// Recover reattaches to every running shim and closes turns whose harness
// is gone. Call it once at daemon start.
func (m *Manager) Recover(ctx context.Context) error {
	sessions, err := m.cfg.Store.Sessions(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sess := range sessions {
		if sess.ArchivedAt != nil {
			continue
		}
		if _, err := m.attach(ctx, sess, false); err == nil {
			continue
		}
		if err := m.abortOpenTurn(ctx, sess.ID, "the harness was not running when the daemon started"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// abortOpenTurn records TurnEnded{aborted} if the store still has an open
// turn; the store is the source of truth for turn state.
func (m *Manager) abortOpenTurn(ctx context.Context, id proto.SessionID, why string) error {
	last, open, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil || !open {
		return err
	}
	_, err = m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: id, Turn: last,
		Entries: []store.Entry{{Payload: proto.TurnEnded{Reason: proto.TurnAborted, Message: why}}},
	})
	if err == nil {
		m.notify(id)
	}
	return err
}

// Close detaches from every harness without stopping it, as a daemon that
// exits does; running turns continue in their shims.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closing = true
	lives := make([]*live, 0, len(m.live))
	for _, l := range m.live {
		lives = append(lives, l)
	}
	m.mu.Unlock()
	var errs []error
	for _, l := range lives {
		errs = append(errs, l.run.Close())
	}
	m.wg.Wait()
	m.cancel()
	return errors.Join(errs...)
}

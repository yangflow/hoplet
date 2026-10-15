package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// Errors a caller maps to refusals. The core refuses most of them before forwarding; the agent repeats the
// checks because its state is authoritative.
var (
	ErrBusy              = errors.New("busy")     // a turn is running or waits for a permission
	ErrStarting          = errors.New("starting") // the process is starting
	ErrExternal          = errors.New("external") // the session file was written by another process
	ErrNotRunning        = errors.New("not-running")
	ErrUnknownRequest    = errors.New("unknown-permission-request")
	ErrSessionNotFound   = errors.New("session-not-on-this-device")
	ErrOptionNotDeclared = errors.New("an option the driver does not declare")
)

// Location is where a session lives on this machine.
type Location struct {
	Dir  string // working directory the process runs in
	File string // the session file; empty when the vendor has none
}

// Locator resolves a session id.
type Locator func(sessionID string) (Location, error)

// Event is what the Manager reports; the daemon turns it into wire messages.
type Event interface{ runtimeEvent() }

// StateEvent reports the state of one session, with the driver's capabilities.
type StateEvent struct {
	SessionID    string
	State        State
	Error        string // set in StateFailed
	Capabilities []Capability
	Models       []string
	// Model is the model the session's processes start with or were switched to; "" is the vendor's default.
	Model string
}

// OutputEvent is streamed content of a session's running turn.
type OutputEvent struct {
	SessionID string
	Output    Output
}

// PermissionEvent is an open permission request of a session.
type PermissionEvent struct {
	SessionID string
	Request   PermissionRequest
}

func (StateEvent) runtimeEvent()      {}
func (OutputEvent) runtimeEvent()     {}
func (PermissionEvent) runtimeEvent() {}

// Config configures a Manager. Zero durations take the defaults of docs/hosting.md (agent configuration).
type Config struct {
	Driver Driver
	Locate Locator
	// Emit receives events in order. It is called with the Manager's lock held, so it must not block and
	// must not call the Manager.
	Emit func(Event)
	// TurnDone is called when a turn ended, however it ended, with the Manager's lock held like Emit: it must
	// not block and must not call the Manager.
	TurnDone func(sessionID string)

	IdleTimeout       time.Duration // stop a process this long after its last turn; default 30 minutes
	MaxIdle           int           // processes kept `ready` without a turn; the least recently used stops first
	PermissionTimeout time.Duration // deny an unanswered permission request after this long; default 10 minutes
	KillAfter         time.Duration // kill a process that did not exit this long after its input closed; default 10 s
	ExternalQuiet     time.Duration // refuse prompts until the session file has not grown this long; default 60 s
	StartTimeout      time.Duration // a process that is not ready after this long is killed; default 60 s
}

// Manager owns the processes of one agent: one per controlled session.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
	wg       sync.WaitGroup
}

type session struct {
	id    string
	state State
	err   string

	proc    Process
	gen     int // generation of proc, to ignore events of a replaced process
	cancel  context.CancelFunc
	loc     Location
	stopped bool // the process was asked to stop

	// fresh: the vendor has not created the session yet, so the first process creates it instead of resuming.
	fresh bool
	opts  StartOptions // chosen when the session was created; every process of the session starts with them

	pendingPrompt *Prompt
	interrupted   bool
	turnErr       string

	perms map[string]*time.Timer // open permission requests by id, each with its auto-deny timer
	reqs  map[string]PermissionRequest

	lastUsed  time.Time
	idleTimer *time.Timer
	killTimer *time.Timer

	// External detection (ADR 0008): recorded is the file size after the last turn result, known when
	// haveRecorded. In StateExternal, lastSize and lastGrowth track the foreign writer.
	recorded     int64
	haveRecorded bool
	lastSize     int64
	lastGrowth   time.Time
}

func NewManager(cfg Config) *Manager {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.PermissionTimeout <= 0 {
		cfg.PermissionTimeout = 10 * time.Minute
	}
	if cfg.KillAfter <= 0 {
		cfg.KillAfter = 10 * time.Second
	}
	if cfg.ExternalQuiet <= 0 {
		cfg.ExternalQuiet = 60 * time.Second
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 60 * time.Second
	}
	if cfg.MaxIdle < 0 {
		cfg.MaxIdle = 0
	}
	if cfg.Emit == nil {
		cfg.Emit = func(Event) {}
	}
	if cfg.TurnDone == nil {
		cfg.TurnDone = func(string) {}
	}
	return &Manager{cfg: cfg, sessions: map[string]*session{}}
}

func (m *Manager) get(id string) *session {
	s := m.sessions[id]
	if s == nil {
		s = &session{id: id, state: StateIdle, perms: map[string]*time.Timer{}, reqs: map[string]PermissionRequest{}}
		m.sessions[id] = s
	}
	return s
}

func (m *Manager) setState(s *session, st State, errText string) {
	if s.state == st && s.err == errText {
		return
	}
	s.state, s.err = st, errText
	m.cfg.Emit(m.stateEvent(s))
}

func (m *Manager) stateEvent(s *session) StateEvent {
	return StateEvent{
		SessionID: s.id, State: s.state, Error: s.err,
		Capabilities: m.cfg.Driver.Capabilities(), Models: m.cfg.Driver.Models(),
		Model: s.opts.Model,
	}
}

// Driver is the vendor driver the Manager starts processes with.
func (m *Manager) Driver() Driver { return m.cfg.Driver }

// States lists the sessions that are not plainly idle, for the report after a reconnect.
func (m *Manager) States() []StateEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StateEvent
	for _, s := range m.sessions {
		if s.state == StateIdle {
			continue
		}
		out = append(out, m.stateEvent(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// State is the state of one session.
func (m *Manager) State(sessionID string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[sessionID]; s != nil {
		return s.state
	}
	return StateIdle
}

// Pending lists the open permission requests of every session, for the report after a reconnect.
func (m *Manager) Pending() []PermissionEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PermissionEvent
	for _, s := range m.sessions {
		for _, req := range s.reqs {
			out = append(out, PermissionEvent{SessionID: s.id, Request: req})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].Request.RequestID < out[j].Request.RequestID
	})
	return out
}

func fileSize(path string) (int64, bool) {
	if path == "" {
		return 0, false
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return st.Size(), true
}

// Observe tells the Manager the size of a session's file as of a scan. A size beyond the recorded one while
// no turn runs is a foreign write; the session becomes `external` and its process stops. While a session is
// `external`, Observe returns it to `idle` once the file has not grown for ExternalQuiet.
func (m *Manager) Observe(sessionID string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.observe(m.get(sessionID), size)
}

func (m *Manager) observe(s *session, size int64) {
	now := time.Now()
	switch s.state {
	case StateStarting, StateRunning, StateWaitingPermission:
		return // the process writes the file during a turn; the next turn result records it
	case StateExternal:
		if size != s.lastSize {
			s.lastSize, s.lastGrowth = size, now
			return
		}
		if now.Sub(s.lastGrowth) >= m.cfg.ExternalQuiet {
			s.recorded, s.haveRecorded = size, true
			m.setState(s, StateIdle, "")
		}
		return
	}
	if !s.haveRecorded {
		s.recorded, s.haveRecorded = size, true
		return
	}
	if size > s.recorded {
		m.markExternal(s, size, now)
	}
}

func (m *Manager) markExternal(s *session, size int64, now time.Time) {
	s.lastSize, s.lastGrowth = size, now
	m.stopProcess(s)
	s.pendingPrompt = nil
	m.setState(s, StateExternal, "")
}

// record stores the session file's size as Hoplet's own.
func (m *Manager) record(s *session) {
	if s.loc.File == "" && m.cfg.Locate != nil {
		if loc, err := m.cfg.Locate(s.id); err == nil {
			s.loc = loc
		}
	}
	if size, ok := fileSize(s.loc.File); ok {
		s.recorded, s.haveRecorded = size, true
	}
}

// Prompt sends a turn to the session's process, starting one when there is none. It returns when the
// request is accepted; progress is reported through Emit.
func (m *Manager) Prompt(sessionID, text string) error {
	return m.PromptWith(sessionID, Prompt{Text: text})
}

// PromptWith is Prompt with images; a driver that does not declare images refuses them with
// ErrOptionNotDeclared.
func (m *Manager) PromptWith(sessionID string, p Prompt) error {
	if err := m.checkImages(p); err != nil {
		return err
	}
	if err := m.checkModel(p.Model); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("runtime is shutting down")
	}
	s := m.get(sessionID)
	switch s.state {
	case StateStarting:
		return ErrStarting
	case StateRunning, StateWaitingPermission:
		return ErrBusy
	}
	loc, err := m.cfg.Locate(sessionID)
	if err != nil {
		return err
	}
	s.loc = loc
	if size, ok := fileSize(loc.File); ok {
		m.observe(s, size)
		if s.state == StateExternal {
			return ErrExternal
		}
	}
	if s.proc != nil && s.stopped { // an earlier process is still exiting
		return ErrStarting
	}
	// A new model holds from this turn on: a running process switches, a new one starts with it.
	if p.Model != "" && p.Model != s.opts.Model {
		if s.proc != nil {
			if err := s.proc.SetModel(p.Model); err != nil {
				m.fail(s, fmt.Sprintf("set model: %v", err))
				return nil
			}
		}
		s.opts.Model = p.Model
	}
	if s.proc != nil {
		return m.writePrompt(s, p)
	}
	m.startProcess(s, p)
	return nil
}

// Create starts a new session in dir with the options and the first prompt, and returns its ID. The vendor
// creates the session's file when it takes the prompt; progress is reported through Emit like any session's.
// An option the driver does not declare is ErrOptionNotDeclared.
func (m *Manager) Create(dir, text string, opts StartOptions) (string, error) {
	return m.CreateWith(dir, Prompt{Text: text}, opts)
}

// CreateWith is Create with images in the first prompt.
func (m *Manager) CreateWith(dir string, p Prompt, opts StartOptions) (string, error) {
	if err := m.checkOptions(opts); err != nil {
		return "", err
	}
	if err := m.checkImages(p); err != nil {
		return "", err
	}
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", errors.New("runtime is shutting down")
	}
	s := m.get(id)
	s.loc = Location{Dir: dir}
	s.fresh, s.opts = true, opts
	m.startProcess(s, p)
	return id, nil
}

// checkModel refuses a model change unless the driver declares it and lists the model.
func (m *Manager) checkModel(model string) error {
	if model == "" {
		return nil
	}
	declared := false
	for _, c := range m.cfg.Driver.Capabilities() {
		declared = declared || c == CapSetModel
	}
	if declared {
		for _, id := range m.cfg.Driver.Models() {
			if id == model {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: model %q", ErrOptionNotDeclared, model)
}

// checkImages refuses images unless the driver declares them.
func (m *Manager) checkImages(p Prompt) error {
	if len(p.Images) == 0 {
		return nil
	}
	for _, c := range m.cfg.Driver.Capabilities() {
		if c == CapImages {
			return nil
		}
	}
	return fmt.Errorf("%w: images", ErrOptionNotDeclared)
}

func (m *Manager) checkOptions(o StartOptions) error {
	declared := map[Capability]bool{}
	for _, c := range m.cfg.Driver.Capabilities() {
		declared[c] = true
	}
	if o.Thinking != nil && !declared[CapThinking] {
		return fmt.Errorf("%w: thinking", ErrOptionNotDeclared)
	}
	if o.Fast && !declared[CapFast] {
		return fmt.Errorf("%w: fast", ErrOptionNotDeclared)
	}
	if o.Model != "" {
		for _, id := range m.cfg.Driver.Models() {
			if id == o.Model {
				return nil
			}
		}
		return fmt.Errorf("%w: model %q", ErrOptionNotDeclared, o.Model)
	}
	return nil
}

// newSessionID makes a random version 4 UUID, the form Claude Code accepts for --session-id.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Info is the state of one session with the error that explains `failed`.
func (m *Manager) Info(sessionID string) (State, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[sessionID]; s != nil {
		return s.state, s.err
	}
	return StateIdle, ""
}

// Discard stops a session's process and forgets the session, which the agent does when a session it started
// could not be completed. It reports whether the process is gone.
func (m *Manager) Discard(sessionID string, timeout time.Duration) bool {
	gone := m.StopAndWait(sessionID, timeout)
	m.mu.Lock()
	defer m.mu.Unlock()
	if gone {
		delete(m.sessions, sessionID)
	}
	return gone
}

func (m *Manager) writePrompt(s *session, p Prompt) error {
	s.interrupted, s.turnErr = false, ""
	if err := s.proc.Prompt(p); err != nil {
		m.fail(s, fmt.Sprintf("write prompt: %v", err))
		return nil
	}
	m.stopTimers(s)
	s.lastUsed = time.Now()
	m.setState(s, StateRunning, "")
	return nil
}

func (m *Manager) startProcess(s *session, p Prompt) {
	s.gen++
	gen := s.gen
	s.stopped = false
	s.pendingPrompt = &p
	s.interrupted, s.turnErr = false, ""
	m.setState(s, StateStarting, "")
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	spec := StartSpec{SessionID: s.id, Dir: s.loc.Dir, New: s.fresh, Options: s.opts}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		proc, err := m.cfg.Driver.Start(ctx, spec)
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.gen != gen {
			if proc != nil {
				_ = proc.Kill()
			}
			return
		}
		if err != nil {
			s.cancel = nil
			s.pendingPrompt = nil
			m.setState(s, StateFailed, err.Error())
			return
		}
		s.proc = proc
		s.fresh = false
		time.AfterFunc(m.cfg.StartTimeout, func() { m.startTimedOut(s, gen) })
		m.wg.Add(1)
		go m.pump(s, gen, proc)
	}()
}

func (m *Manager) startTimedOut(s *session, gen int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.gen != gen || s.state != StateStarting || s.proc == nil {
		return
	}
	s.pendingPrompt = nil
	m.fail(s, "the process did not become ready in time")
}

func (m *Manager) pump(s *session, gen int, proc Process) {
	defer m.wg.Done()
	for ev := range proc.Events() {
		m.mu.Lock()
		if s.gen == gen {
			m.handle(s, ev)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) handle(s *session, ev ProcessEvent) {
	if _, isExit := ev.(Exited); s.stopped && !isExit {
		return // a process that was asked to stop no longer changes the state
	}
	switch ev := ev.(type) {
	case Ready:
		if s.state != StateStarting {
			return
		}
		if s.pendingPrompt != nil {
			p := *s.pendingPrompt
			s.pendingPrompt = nil
			_ = m.writePrompt(s, p)
			return
		}
		m.becomeReady(s)
	case StateChanged:
		if s.state == StateStarting {
			return
		}
		switch ev.State {
		case StateRunning:
			m.stopTimers(s)
			m.setState(s, StateRunning, "")
		case StateWaitingPermission:
			m.setState(s, StateWaitingPermission, "")
		case StateReady:
			m.record(s)
			if s.turnErr != "" {
				msg := s.turnErr
				s.turnErr = ""
				m.setState(s, StateFailed, msg)
				m.becomeIdleProcess(s)
				return
			}
			m.becomeReady(s)
		}
	case Output:
		m.cfg.Emit(OutputEvent{SessionID: s.id, Output: ev})
	case PermissionRequest:
		m.addPermission(s, ev)
	case PermissionCancelled:
		m.dropPermission(s, ev.RequestID)
	case TurnResult:
		m.record(s)
		m.clearPermissions(s)
		if ev.Failed && !s.interrupted {
			s.turnErr = ev.Error
		}
		s.interrupted = false
		m.cfg.TurnDone(s.id)
	case Exited:
		m.exited(s, ev)
	}
}

func (m *Manager) becomeReady(s *session) {
	m.setState(s, StateReady, "")
	m.becomeIdleProcess(s)
}

// becomeIdleProcess starts the idle timer of a process without a turn and enforces the idle cap.
func (m *Manager) becomeIdleProcess(s *session) {
	s.lastUsed = time.Now()
	gen := s.gen
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(m.cfg.IdleTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.gen == gen && s.proc != nil && (s.state == StateReady || s.state == StateFailed) {
			m.stopProcess(s)
		}
	})
	m.enforceIdleCap()
}

// enforceIdleCap stops the least recently used processes while more than MaxIdle are without a turn.
func (m *Manager) enforceIdleCap() {
	var idle []*session
	for _, s := range m.sessions {
		if s.proc != nil && !s.stopped && (s.state == StateReady || s.state == StateFailed) {
			idle = append(idle, s)
		}
	}
	if len(idle) <= m.cfg.MaxIdle {
		return
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].lastUsed.Before(idle[j].lastUsed) })
	for _, s := range idle[:len(idle)-m.cfg.MaxIdle] {
		m.stopProcess(s)
	}
}

func (m *Manager) stopTimers(s *session) {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

// stopProcess closes the process's input and kills it when it does not exit in time.
func (m *Manager) stopProcess(s *session) {
	m.stopTimers(s)
	m.clearPermissions(s)
	if s.cancel != nil && s.proc == nil {
		s.cancel() // still starting
		s.gen++
		s.cancel = nil
		s.pendingPrompt = nil
		if s.state == StateStarting {
			m.setState(s, StateIdle, "")
		}
		return
	}
	if s.proc == nil || s.stopped {
		return
	}
	s.stopped = true
	s.pendingPrompt = nil
	proc, gen := s.proc, s.gen
	_ = proc.Close()
	s.killTimer = time.AfterFunc(m.cfg.KillAfter, func() {
		m.mu.Lock()
		stillThere := s.gen == gen && s.proc == proc
		m.mu.Unlock()
		if stillThere {
			_ = proc.Kill()
		}
	})
}

// fail reports an error and ends the process.
func (m *Manager) fail(s *session, msg string) {
	m.stopProcess(s)
	m.setState(s, StateFailed, msg)
}

func (m *Manager) exited(s *session, ev Exited) {
	if s.killTimer != nil {
		s.killTimer.Stop()
		s.killTimer = nil
	}
	m.stopTimers(s)
	m.clearPermissions(s)
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	wasStopped := s.stopped
	s.proc, s.stopped, s.pendingPrompt = nil, false, nil
	s.interrupted = false
	m.record(s)
	switch {
	case s.state == StateExternal || (wasStopped && s.state == StateFailed):
		// the state already says why the process ended
	case wasStopped:
		m.setState(s, StateIdle, "")
	default:
		msg := "the process exited"
		if ev.Err != nil {
			msg = fmt.Sprintf("the process exited: %v", ev.Err)
		}
		if s.turnErr != "" {
			msg = s.turnErr
		}
		s.turnErr = ""
		m.setState(s, StateFailed, msg)
	}
}

func (m *Manager) addPermission(s *session, req PermissionRequest) {
	id := req.RequestID
	gen := s.gen
	s.reqs[id] = req
	s.perms[id] = time.AfterFunc(m.cfg.PermissionTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.gen == gen {
			_ = m.answer(s, id, false, "No answer within "+m.cfg.PermissionTimeout.String()+"; denied by Hoplet")
		}
	})
	if s.state != StateWaitingPermission {
		m.setState(s, StateWaitingPermission, "")
	}
	m.cfg.Emit(PermissionEvent{SessionID: s.id, Request: req})
}

func (m *Manager) dropPermission(s *session, id string) {
	if t, ok := s.perms[id]; ok {
		t.Stop()
		delete(s.perms, id)
		delete(s.reqs, id)
	}
}

func (m *Manager) clearPermissions(s *session) {
	for id := range s.perms {
		m.dropPermission(s, id)
	}
}

func (m *Manager) answer(s *session, id string, allow bool, message string) error {
	if _, ok := s.perms[id]; !ok || s.proc == nil {
		return ErrUnknownRequest
	}
	m.dropPermission(s, id)
	return s.proc.Answer(id, allow, message)
}

// Permission answers an open permission request. A request that is not open (answered, denied by timeout,
// cancelled) is ErrUnknownRequest.
func (m *Manager) Permission(sessionID, requestID string, allow bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil {
		return ErrUnknownRequest
	}
	msg := ""
	if !allow {
		msg = "Denied by the user through Hoplet"
	}
	return m.answer(s, requestID, allow, msg)
}

// Interrupt ends the running turn and keeps the process.
func (m *Manager) Interrupt(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil || s.proc == nil || (s.state != StateRunning && s.state != StateWaitingPermission) {
		return ErrNotRunning
	}
	s.interrupted = true
	return s.proc.Interrupt()
}

// Stop ends the session's process; the state becomes `idle` when it exited.
func (m *Manager) Stop(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[sessionID]; s != nil {
		m.stopProcess(s)
	}
}

// StopAndWait stops the session's process and waits until it exited or the timeout passed; it reports
// whether the session has no process. Handoff uses it before the session file is exported.
func (m *Manager) StopAndWait(sessionID string, timeout time.Duration) bool {
	m.Stop(sessionID)
	deadline := time.Now().Add(timeout)
	for {
		m.mu.Lock()
		s := m.sessions[sessionID]
		gone := s == nil || (s.proc == nil && s.cancel == nil)
		m.mu.Unlock()
		if gone {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Shutdown stops every process and waits for them, up to ctx.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	for _, s := range m.sessions {
		m.stopProcess(s)
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		m.mu.Lock()
		for _, s := range m.sessions {
			if s.proc != nil {
				_ = s.proc.Kill()
			}
		}
		m.mu.Unlock()
	}
}

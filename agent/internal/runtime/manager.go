package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"reflect"
	"slices"
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
	ErrSteerUnsupported  = errors.New("unsupported") // the driver does not declare steer
	ErrPermissionPending = errors.New("permission")  // a permission request waits for an answer
	ErrUnknownRequest    = errors.New("unknown-permission-request")
	ErrSessionNotFound   = errors.New("session-not-on-this-device")
	ErrOptionNotDeclared = errors.New("an option the driver does not declare")
	ErrInvalidMode       = errors.New("a mode the plan request does not offer")
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
	// Config is the driver's settings, each with this session's value as Current.
	Config []ConfigOption
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

// CommandsEvent is the complete list of commands and skills of a session, with its working directory. It is
// reported when the list changes.
type CommandsEvent struct {
	SessionID string
	Dir       string
	Commands  []Command
}

func (CommandsEvent) runtimeEvent()   {}
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
	// TurnStart is called when a turn begins for a session (a prompt is accepted), under the same rules as
	// TurnDone. It can be called twice for one turn when the process has to start first.
	TurnStart func(sessionID string)

	IdleTimeout       time.Duration // stop a process this long after its last turn; default 30 minutes
	MaxIdle           int           // processes kept `ready` without a turn; the least recently used stops first
	PermissionTimeout time.Duration // deny an unanswered permission request after this long; default 10 minutes
	KillAfter         time.Duration // kill a process that did not exit this long after its input closed; default 10 s
	ExternalQuiet     time.Duration // refuse prompts until the session file has not grown this long; default 60 s
	StartTimeout      time.Duration // a process that is not ready after this long is killed; default 60 s
	OwnGrace          time.Duration // growth this long after a turn result is the process's own; default 5 s
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
	// vals are the session's value of every setting by option ID, nil until the session first needs them; every
	// process of the session starts with them. changed says a value was set, so the session is reported even
	// when idle.
	vals    map[string]any
	changed bool
	// started holds the values the current process has: those it started with, then those it switched to. What
	// differs from vals is a change the process has not taken yet.
	started map[string]any
	// setSeq is the number of the latest user change of each setting and inflight the changes of it a process
	// has not answered yet; seq numbers them.
	setSeq   map[string]int
	inflight map[string]int
	seq      int

	// commands is the latest list the session's process reported; it stays after the process exits.
	commands []Command

	pendingPrompt *Prompt
	// restartWith is the prompt that waits for the process to exit and a new one to start with the new values.
	restartWith *Prompt
	interrupted bool
	turnErr     string

	perms map[string]*time.Timer // open permission requests by id, each with its auto-deny timer
	reqs  map[string]PermissionRequest

	lastUsed  time.Time
	idleTimer *time.Timer
	killTimer *time.Timer

	// External detection (ADR 0008): recorded is the file size after the last turn result, known when
	// haveRecorded. In StateExternal, lastSize and lastGrowth track the foreign writer.
	recorded     int64
	haveRecorded bool
	// ownUntil ends the grace after a turn result in which the process still writes its trailing lines (title,
	// cost): growth until then is its own, and ownSeq tells the timer that settles it which grace it belongs to.
	ownUntil   time.Time
	ownSeq     int
	lastSize   int64
	lastGrowth time.Time
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
	if cfg.OwnGrace <= 0 {
		cfg.OwnGrace = 5 * time.Second
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
	if cfg.TurnStart == nil {
		cfg.TurnStart = func(string) {}
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
		Capabilities: m.cfg.Driver.Capabilities(), Config: m.configOf(s),
	}
}

// configOf is the driver's settings with the session's values as Current.
func (m *Manager) configOf(s *session) []ConfigOption {
	out := m.cfg.Driver.Config()
	for i := range out {
		if v, ok := s.vals[out[i].ID]; ok {
			out[i].Current = v
		}
	}
	return out
}

// valuesOf is the session's values, starting from the driver's defaults.
func (m *Manager) valuesOf(s *session) map[string]any {
	if s.vals == nil {
		s.vals = map[string]any{}
		for _, o := range m.cfg.Driver.Config() {
			s.vals[o.ID] = o.Current
		}
	}
	return s.vals
}

// option is the driver's declaration of a setting.
func (m *Manager) option(id string) (ConfigOption, bool) {
	for _, o := range m.cfg.Driver.Config() {
		if o.ID == id {
			return o, true
		}
	}
	return ConfigOption{}, false
}

// checkValue refuses a value the driver's declaration of the setting does not accept.
func checkValue(o ConfigOption, value any) error {
	switch o.Type {
	case ConfigBoolean:
		if _, ok := value.(bool); ok {
			return nil
		}
	case ConfigSelect:
		if v, ok := value.(string); ok {
			for _, c := range o.Options {
				if c.Value == v {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("%w: %s=%v", ErrOptionNotDeclared, o.ID, value)
}

// checkConfig refuses values for settings the driver does not declare or declares otherwise.
func (m *Manager) checkConfig(values map[string]any) error {
	for id, value := range values {
		o, ok := m.option(id)
		if !ok {
			return fmt.Errorf("%w: %s", ErrOptionNotDeclared, id)
		}
		if err := checkValue(o, value); err != nil {
			return err
		}
	}
	return nil
}

// Knows reports whether the Manager holds state for the session: a process runs for it, or one ran in this run.
func (m *Manager) Knows(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[sessionID]
	return ok
}

// Driver is the vendor driver the Manager starts processes with.
func (m *Manager) Driver() Driver { return m.cfg.Driver }

// States lists the sessions that are not plainly idle (or have a value set), for the report after a reconnect.
func (m *Manager) States() []StateEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StateEvent
	for _, s := range m.sessions {
		if s.state == StateIdle && !s.changed {
			continue
		}
		out = append(out, m.stateEvent(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

func (m *Manager) commandsEvent(s *session) CommandsEvent {
	return CommandsEvent{SessionID: s.id, Dir: s.loc.Dir, Commands: s.commands}
}

// Commands lists the commands of every session that has reported some, for the report after a reconnect.
func (m *Manager) Commands() []CommandsEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CommandsEvent
	for _, s := range m.sessions {
		if len(s.commands) > 0 {
			out = append(out, m.commandsEvent(s))
		}
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

// PID is the process ID of the session's running process, 0 when it has none or the driver does not expose it.
// It only scopes the scan for dev server ports (ADR 0026) and is never reported.
func (m *Manager) PID(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[sessionID]; s != nil && s.proc != nil {
		if p, ok := s.proc.(interface{ PID() int }); ok {
			return p.PID()
		}
	}
	return 0
}

// PIDs maps each session with a running process to its process ID. Like PID, it only feeds the measurement of
// the machine's resources and is never reported.
func (m *Manager) PIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for id, s := range m.sessions {
		if s.proc == nil {
			continue
		}
		if p, ok := s.proc.(interface{ PID() int }); ok {
			if pid := p.PID(); pid > 0 {
				out[id] = pid
			}
		}
	}
	return out
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
		if now.Before(s.ownUntil) {
			return // settled by the timer of the grace
		}
		m.markExternal(s, size, now)
	}
}

// grace opens the window after a turn result in which growth is the process's own: Claude Code appends lines
// such as the title and the cost after the result. When it ends, the file's size is recorded, so a scan never
// sees those lines as a foreign write; a write by another process after it is still detected.
func (m *Manager) grace(s *session) {
	s.ownSeq++
	seq := s.ownSeq
	s.ownUntil = time.Now().Add(m.cfg.OwnGrace)
	time.AfterFunc(m.cfg.OwnGrace, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.ownSeq != seq || m.closed {
			return
		}
		switch s.state {
		case StateIdle, StateReady, StateFailed:
			m.record(s)
		}
	})
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
	if s.proc != nil {
		return m.promptRunning(s, p)
	}
	m.startProcess(s, p)
	return nil
}

// Steer injects a message into the running turn (ADR 0020). It needs the driver's steer capability and a
// process in `running`: while a permission request waits, Claude Code does not process a message reliably,
// so that is refused. The state stays as it is: the message is part of the turn, or the vendor runs it as the
// next one, which the state events report.
func (m *Manager) Steer(sessionID string, p Prompt) error {
	if !slices.Contains(m.cfg.Driver.Capabilities(), CapSteer) {
		return ErrSteerUnsupported
	}
	if err := m.checkImages(p); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("runtime is shutting down")
	}
	s := m.sessions[sessionID]
	if s == nil || s.proc == nil || s.stopped {
		return ErrNotRunning
	}
	switch s.state {
	case StateWaitingPermission:
		return ErrPermissionPending
	case StateRunning:
	default:
		return ErrNotRunning
	}
	if err := s.proc.Steer(p); err != nil {
		return fmt.Errorf("write steer: %w", err)
	}
	s.lastUsed = time.Now()
	return nil
}

// promptRunning writes a prompt to the session's ready process. The values hold from this turn on: a change an
// ApplyRestart setting made restarts the process between turns with the values as flags, and the others are
// switched in place before the prompt.
func (m *Manager) promptRunning(s *session, p Prompt) error {
	restart, err := m.reconcile(s)
	if err != nil {
		m.fail(s, fmt.Sprintf("set %v", err))
		return nil
	}
	if restart {
		s.restartWith = &p
		m.setState(s, StateStarting, "")
		m.stopProcess(s)
		return nil
	}
	return m.writePrompt(s, p)
}

// reconcile brings the process up to the session's values: it switches the ApplyNow and ApplyNextTurn settings
// that differ, in the driver's order, and says whether an ApplyRestart setting differs, which only a restart
// applies.
func (m *Manager) reconcile(s *session) (restart bool, err error) {
	var switches []ConfigOption
	for _, o := range m.cfg.Driver.Config() {
		if s.vals[o.ID] == s.started[o.ID] {
			continue
		}
		if o.Apply == ApplyRestart {
			return true, nil
		}
		switches = append(switches, o)
	}
	for _, o := range switches {
		if err := s.proc.Set(o.ID, s.vals[o.ID]); err != nil {
			return false, fmt.Errorf("%s: %w", o.ID, err)
		}
		s.started[o.ID] = s.vals[o.ID]
	}
	return false, nil
}

// SetConfig sets one setting of a session (ADR 0018) and reports the session's values. It works whatever the
// session's state and with or without a process. With a process, an ApplyNow setting is switched at once, an
// ApplyNextTurn one before the next prompt is written, and an ApplyRestart one restarts the process when the
// next prompt arrives; without one, the next process starts with the value.
func (m *Manager) SetConfig(sessionID, id string, value any) error {
	err := m.setConfig(sessionID, id, value)
	if err != nil {
		log.Printf("[agent] session %s: set %s=%v failed: %v", sessionID, id, value, err)
	}
	return err
}

func (m *Manager) setConfig(sessionID, id string, value any) error {
	o, ok := m.option(id)
	if !ok {
		return fmt.Errorf("%w: %s", ErrOptionNotDeclared, id)
	}
	if err := checkValue(o, value); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("runtime is shutting down")
	}
	s := m.get(sessionID)
	vals := m.valuesOf(s)
	old := vals[id]
	vals[id] = value
	s.changed = true
	// Every other change waits in vals for the process to take it: before the next prompt, or when a process
	// that is still starting is ready.
	if o.Apply != ApplyNow || s.proc == nil || s.stopped || s.state == StateStarting {
		log.Printf("[agent] session %s: set %s=%v", sessionID, id, value)
		m.cfg.Emit(m.stateEvent(s))
		m.mu.Unlock()
		return nil
	}
	// The process answers an ApplyNow switch only after the manager has taken the events it sent before, so
	// the wait is outside the lock: a lock held across it stalls every session behind a busy stream.
	proc, gen := s.proc, s.gen
	s.seq++
	mine := s.seq
	if s.setSeq == nil {
		s.setSeq = map[string]int{}
		s.inflight = map[string]int{}
	}
	s.setSeq[id] = mine
	s.inflight[id]++
	m.mu.Unlock()

	err := proc.Set(id, value)

	m.mu.Lock()
	defer m.mu.Unlock()
	s.inflight[id]--
	if s.gen != gen || s.proc != proc {
		return nil // the process was replaced: the next one starts with the session's values
	}
	if s.setSeq[id] != mine {
		if err != nil {
			return fmt.Errorf("set %s: %w", id, err) // a newer change of the setting decides the value
		}
		return nil
	}
	if err != nil {
		// The reported value is the one the process has.
		if cur, ok := s.started[id]; ok {
			old = cur
		}
		s.vals[id] = old
		m.cfg.Emit(m.stateEvent(s))
		return fmt.Errorf("set %s: %w", id, err)
	}
	s.started[id] = value
	log.Printf("[agent] session %s: set %s=%v", sessionID, id, value)
	m.cfg.Emit(m.stateEvent(s))
	return nil
}

// configChanged takes a setting the vendor changed itself. The value becomes the session's unless a change of
// the user waits for the process to take it; either way it is what the process has now.
func (m *Manager) configChanged(s *session, ev ConfigChanged) {
	o, ok := m.option(ev.ID)
	if !ok || checkValue(o, ev.Value) != nil {
		log.Printf("[agent] session %s: ignored %s=%v, which the driver does not declare", s.id, ev.ID, ev.Value)
		return
	}
	vals := m.valuesOf(s)
	if s.started == nil {
		s.started = map[string]any{}
	}
	pending := vals[ev.ID] != s.started[ev.ID] || s.inflight[ev.ID] > 0
	s.started[ev.ID] = ev.Value
	if pending || vals[ev.ID] == ev.Value {
		return
	}
	vals[ev.ID] = ev.Value
	s.changed = true
	log.Printf("[agent] session %s: vendor set %s=%v", s.id, ev.ID, ev.Value)
	m.cfg.Emit(m.stateEvent(s))
}

// Create starts a new session in dir with the settings in config (an option left out keeps its default) and
// the first prompt, and returns its ID. The vendor creates the session's file when it takes the prompt;
// progress is reported through Emit like any session's. A setting the driver does not declare is
// ErrOptionNotDeclared.
func (m *Manager) Create(dir, text string, config map[string]any) (string, error) {
	return m.CreateWith(dir, Prompt{Text: text}, config)
}

// CreateWith is Create with images in the first prompt.
func (m *Manager) CreateWith(dir string, p Prompt, config map[string]any) (string, error) {
	if err := m.checkConfig(config); err != nil {
		return "", err
	}
	if err := m.checkImages(p); err != nil {
		return "", err
	}
	if a, ok := m.cfg.Driver.(IDAssigner); ok {
		return m.createAssigned(a, dir, p, config)
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
	s.fresh = true
	for k, v := range config {
		m.valuesOf(s)[k] = v
	}
	m.valuesOf(s)
	m.startProcess(s, p)
	return id, nil
}

// createAssigned is CreateWith for a driver whose vendor assigns the ID: the driver creates the session before
// the manager knows its ID, so the session is not registered until the driver returns it. A failed start
// leaves nothing behind.
func (m *Manager) createAssigned(a IDAssigner, dir string, p Prompt, config map[string]any) (string, error) {
	s := &session{state: StateIdle, perms: map[string]*time.Timer{}, reqs: map[string]PermissionRequest{}}
	s.loc = Location{Dir: dir}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", errors.New("runtime is shutting down")
	}
	for k, v := range config {
		m.valuesOf(s)[k] = v
	}
	spec := StartSpec{Dir: dir, New: true, Config: maps.Clone(m.valuesOf(s))}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()

	type started struct {
		id   string
		proc Process
		err  error
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan started, 1)
	go func() {
		id, proc, err := a.StartNew(ctx, spec)
		done <- started{id, proc, err}
	}()
	var r started
	select {
	case r = <-done:
	case <-time.After(m.cfg.StartTimeout):
		cancel()
		go func() {
			if late := <-done; late.proc != nil {
				_ = late.proc.Kill()
			}
		}()
		return "", errors.New("the vendor did not create the session in time")
	}
	kill := func(err error) (string, error) {
		cancel()
		if r.proc != nil {
			_ = r.proc.Kill()
		}
		return "", err
	}
	if r.err != nil {
		return kill(r.err)
	}
	if r.id == "" || r.proc == nil {
		return kill(errors.New("the driver returned no session ID"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return kill(errors.New("runtime is shutting down"))
	}
	if m.sessions[r.id] != nil {
		return kill(fmt.Errorf("the vendor assigned %s, which is already a session", r.id))
	}
	s.id = r.id
	m.sessions[s.id] = s
	gen := m.starting(s, p, cancel)
	m.adopt(s, gen, r.proc)
	return s.id, nil
}

func (m *Manager) declares(c Capability) bool {
	for _, d := range m.cfg.Driver.Capabilities() {
		if d == c {
			return true
		}
	}
	return false
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
	m.cfg.TurnStart(s.id)
	return nil
}

func (m *Manager) startProcess(s *session, p Prompt) {
	ctx, cancel := context.WithCancel(context.Background())
	gen := m.starting(s, p, cancel)
	spec := StartSpec{SessionID: s.id, Dir: s.loc.Dir, New: s.fresh, Config: maps.Clone(s.started)}
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
		m.adopt(s, gen, proc)
	}()
}

// starting moves the session to `starting` for a new process that runs p first and returns the process's
// generation. The process starts with every value as it stands now; a change made while it starts is applied
// when it is ready.
func (m *Manager) starting(s *session, p Prompt, cancel context.CancelFunc) int {
	s.gen++
	s.stopped = false
	s.pendingPrompt = &p
	s.interrupted, s.turnErr = false, ""
	m.setState(s, StateStarting, "")
	m.cfg.TurnStart(s.id)
	s.cancel = cancel
	s.started = maps.Clone(m.valuesOf(s))
	return s.gen
}

// adopt makes proc the session's process and starts reading its events.
func (m *Manager) adopt(s *session, gen int, proc Process) {
	s.proc = proc
	s.fresh = false
	time.AfterFunc(m.cfg.StartTimeout, func() { m.startTimedOut(s, gen) })
	m.wg.Add(1)
	go m.pump(s, gen, proc)
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
			_ = m.promptRunning(s, p)
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
	case ConfigChanged:
		m.configChanged(s, ev)
	case Commands:
		if len(s.commands) == 0 && len(ev.List) == 0 || reflect.DeepEqual(s.commands, ev.List) {
			return
		}
		s.commands = ev.List
		m.cfg.Emit(m.commandsEvent(s))
	case Output:
		m.cfg.Emit(OutputEvent{SessionID: s.id, Output: ev})
	case PermissionRequest:
		m.addPermission(s, ev)
	case PermissionCancelled:
		m.dropPermission(s, ev.RequestID)
	case TurnResult:
		m.record(s)
		m.grace(s)
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
	if wasStopped && s.restartWith != nil {
		p := *s.restartWith
		s.restartWith = nil
		m.startProcess(s, p)
		return
	}
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
	if req.PlanExit != nil {
		// A plan is read at the user's pace: no automatic denial (ADR 0019).
		s.perms[id] = nil
	} else {
		s.perms[id] = time.AfterFunc(m.cfg.PermissionTimeout, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if s.gen == gen {
				_ = m.answer(s, id, Verdict{Message: "No answer within " + m.cfg.PermissionTimeout.String() + "; denied by Hoplet"})
			}
		})
	}
	if s.state != StateWaitingPermission {
		m.setState(s, StateWaitingPermission, "")
	}
	m.cfg.Emit(PermissionEvent{SessionID: s.id, Request: req})
}

func (m *Manager) dropPermission(s *session, id string) {
	if t, ok := s.perms[id]; ok {
		if t != nil {
			t.Stop()
		}
		delete(s.perms, id)
		delete(s.reqs, id)
	}
}

func (m *Manager) clearPermissions(s *session) {
	for id := range s.perms {
		m.dropPermission(s, id)
	}
}

func (m *Manager) answer(s *session, id string, v Verdict) error {
	if _, ok := s.perms[id]; !ok || s.proc == nil {
		return ErrUnknownRequest
	}
	if v.Mode != "" {
		plan := s.reqs[id].PlanExit
		if plan == nil || !slices.Contains(plan.Modes, v.Mode) || !v.Allow {
			return ErrInvalidMode
		}
	}
	m.dropPermission(s, id)
	return s.proc.Answer(id, v)
}

// Permission answers an open permission request. A request that is not open (answered, denied by timeout,
// cancelled) is ErrUnknownRequest.
func (m *Manager) Permission(sessionID, requestID string, allow bool) error {
	return m.Decide(sessionID, requestID, Verdict{Allow: allow})
}

// Decide answers an open permission request with a verdict: a refusal with the user's words, or the approval
// of a plan with the mode to switch to. A Mode the request does not offer is ErrInvalidMode and leaves the
// request open.
func (m *Manager) Decide(sessionID, requestID string, v Verdict) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil {
		return ErrUnknownRequest
	}
	if !v.Allow && v.Message == "" {
		v.Message = "Denied by the user through Hoplet"
	}
	return m.answer(s, requestID, v)
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
		s.restartWith = nil
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

// ApplyDefaults is opts with the value of each default that the option declares as its Current value; a
// default for an option that does not exist or a value it does not accept is left out.
func ApplyDefaults(opts []ConfigOption, defaults map[string]any) []ConfigOption {
	for i := range opts {
		if v, ok := defaults[opts[i].ID]; ok && checkValue(opts[i], v) == nil {
			opts[i].Current = v
		}
	}
	return opts
}

// DeclaresValue says whether opts declare an option id that accepts value.
func DeclaresValue(opts []ConfigOption, id string, value any) bool {
	for _, o := range opts {
		if o.ID == id {
			return checkValue(o, value) == nil
		}
	}
	return false
}

package terminal

import (
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Events is how a Manager reports to its owner. Callbacks run on the terminal's own goroutines (Opened on the
// caller of Open) and must not call back into the Manager.
type Events struct {
	Opened func(id string)
	Output func(id string, seq int64, data []byte)
	// Closed is called once per opened terminal, after its last Output. exitCode is nil unless the shell
	// exited by itself.
	Closed func(id, reason string, exitCode *int)
}

// Config tunes a Manager; the zero value of every field except Home and Events means the ADR 0026 default.
type Config struct {
	Home          string        // <hoplet_home>: the journal lives in Home/terminal
	DeviceID      string        // for the audit line
	Shell         string        // overrides $SHELL and /bin/sh (tests)
	Clock         Clock         // the idle and max-age timers and the input budget run on it
	KillGrace     time.Duration // SIGHUP to SIGKILL; real time
	IdleTimeout   time.Duration
	MaxAge        time.Duration
	MaxPerMachine int
	FlushEvery    time.Duration // output coalescing; real time
	Window        int           // unacknowledged output allowed
	InputRate     int           // input bytes per second
	Events        Events
	Log           func(format string, args ...any)
}

// Manager owns the terminals of this machine: at most one per session and MaxPerMachine in all.
type Manager struct {
	cfg   Config
	mu    sync.Mutex
	terms map[string]*term
}

// New returns a Manager.
func New(cfg Config) *Manager {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = DefaultGrace
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdle
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultMaxAge
	}
	if cfg.MaxPerMachine <= 0 {
		cfg.MaxPerMachine = PerMachine
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = DefaultFlush
	}
	if cfg.Window <= 0 {
		cfg.Window = WindowBytes
	}
	if cfg.InputRate <= 0 {
		cfg.InputRate = InputRate
	}
	if cfg.Log == nil {
		cfg.Log = log.Printf
	}
	return &Manager{cfg: cfg, terms: map[string]*term{}}
}

type term struct {
	m       *Manager
	id      string
	session string
	opened  time.Time

	// guarded by Manager.mu
	started bool
	abort   string

	finished chan struct{} // closed when the terminal is gone (or its opening was abandoned)
	ready    chan struct{} // closed once Events.Opened has been called
	quit     chan struct{} // closed when the terminal starts to close

	cmd  *exec.Cmd
	ptmx *os.File
	pid  int

	in chan []byte

	mu        sync.Mutex
	cond      *sync.Cond
	pending   []byte
	unacked   int64
	seq       int64
	closing   bool
	flush     bool
	lastInput time.Time
	tokens    float64
	refilled  time.Time

	readerDone chan struct{}
	senderDone chan struct{}
	waitDone   chan struct{}
	exitCode   *int

	once              sync.Once
	bytesIn, bytesOut atomic.Int64
}

// Active is the number of terminals, opening ones included.
func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.terms)
}

// ShellPID is the process ID of the shell of the session's terminal, 0 when there is none. It only scopes the
// scan for dev server ports (ADR 0026) and is never reported.
func (m *Manager) ShellPID(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.terms {
		if t.session == sessionID && t.started {
			return t.pid
		}
	}
	return 0
}

// ShellPIDs maps each session with a started shell to its process ID; it only feeds the measurement of the
// machine's resources and is never reported.
func (m *Manager) ShellPIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, t := range m.terms {
		if t.started && t.pid > 0 {
			out[t.session] = t.pid
		}
	}
	return out
}

// Open starts a shell for the session in dir. guard runs after the slot is reserved and before the shell
// starts; it returns an *OpenError to refuse (the runtime state and the handoff checks of the daemon). A
// refusal is an *OpenError whose Reason goes into terminal.rejected.
func (m *Manager) Open(id, sessionID, dir string, cols, rows int, guard func() error) error {
	if !idPattern.MatchString(id) {
		return &OpenError{ReasonClosed}
	}
	t := &term{
		m: m, id: id, session: sessionID,
		finished: make(chan struct{}), ready: make(chan struct{}), quit: make(chan struct{}),
		in:         make(chan []byte, 32),
		readerDone: make(chan struct{}), senderDone: make(chan struct{}), waitDone: make(chan struct{}),
	}
	t.cond = sync.NewCond(&t.mu)

	m.mu.Lock()
	busy := len(m.terms) >= m.cfg.MaxPerMachine
	for _, o := range m.terms {
		if o.id == id || o.session == sessionID {
			busy = true
		}
	}
	if busy {
		m.mu.Unlock()
		return &OpenError{ReasonBusy}
	}
	m.terms[id] = t
	m.mu.Unlock()

	if err := m.spawn(t, dir, cols, rows, guard); err != nil {
		m.mu.Lock()
		delete(m.terms, id)
		m.mu.Unlock()
		close(t.finished)
		return err
	}
	if m.cfg.Events.Opened != nil {
		m.cfg.Events.Opened(id)
	}
	close(t.ready)
	m.cfg.Log("[terminal] opened session=%s terminal=%s device=%s", sessionID, id, m.cfg.DeviceID)
	return nil
}

func (m *Manager) spawn(t *term, dir string, cols, rows int, guard func() error) error {
	if guard != nil {
		if err := guard(); err != nil {
			if oe, ok := err.(*OpenError); ok {
				return oe
			}
			return &OpenError{ReasonClosed}
		}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return &OpenError{ReasonNotADirectory}
	}
	cols, rows = clamp(cols, 1, MaxCols, defaultCols), clamp(rows, 1, MaxRows, defaultRows)

	cmd := exec.Command(pickShell(m.cfg.Shell))
	cmd.Dir = dir
	cmd.Env = shellEnv(os.Environ())
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return &OpenError{ReasonClosed}
	}
	t.cmd, t.ptmx, t.pid = cmd, ptmx, cmd.Process.Pid

	start, _ := startTime(t.pid)
	if err := writeRecord(m.cfg.Home, record{TerminalID: t.id, PID: t.pid, Start: start, Cwd: dir}); err != nil {
		m.discard(t)
		return &OpenError{ReasonClosed}
	}

	m.mu.Lock()
	if t.abort != "" {
		reason := t.abort
		m.mu.Unlock()
		m.discard(t)
		return &OpenError{reason}
	}
	t.started = true
	t.opened = m.cfg.Clock.Now()
	t.lastInput, t.refilled, t.tokens = t.opened, t.opened, float64(m.cfg.InputRate)
	go t.readLoop()
	go t.sendLoop()
	go t.writeLoop()
	go t.watch()
	go t.wait()
	m.mu.Unlock()
	return nil
}

// discard tears down a shell that never became a terminal.
func (m *Manager) discard(t *term) {
	killGroup(t.pid, syscall.SIGKILL)
	_ = syscall.Kill(t.pid, syscall.SIGKILL)
	_ = t.cmd.Wait()
	_ = t.ptmx.Close()
	removeRecord(m.cfg.Home, t.id)
}

func clamp(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}

// pickShell is the configured shell, else $SHELL when it names an executable file, else /bin/sh.
func pickShell(configured string) string {
	if configured != "" {
		return configured
	}
	if s := os.Getenv("SHELL"); filepath.IsAbs(s) {
		if info, err := os.Stat(s); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return s
		}
	}
	return "/bin/sh"
}

// shellEnv is env without HOPLET_* (the agent's credentials and settings) and with TERM set.
func shellEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOPLET_") || strings.HasPrefix(kv, "TERM=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color")
}

func (m *Manager) lookup(id string) *term {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.terms[id]; t != nil && t.started {
		return t
	}
	return nil
}

// Input writes keystrokes to the terminal; too large or too fast input closes it with input-flood.
func (m *Manager) Input(id string, data []byte) {
	if t := m.lookup(id); t != nil {
		t.input(data)
	}
}

// Resize changes the window size; values outside the limits are ignored.
func (m *Manager) Resize(id string, cols, rows int) {
	if cols < 1 || cols > MaxCols || rows < 1 || rows > MaxRows {
		return
	}
	if t := m.lookup(id); t != nil {
		_ = pty.Setsize(t.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	}
}

// Ack returns n bytes of output credit.
func (m *Manager) Ack(id string, n int64) {
	t := m.lookup(id)
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	t.unacked -= n
	if t.unacked < 0 {
		t.unacked = 0
	}
	t.cond.Broadcast()
	t.mu.Unlock()
}

// Close closes one terminal in the background.
func (m *Manager) Close(id, reason string) {
	m.mu.Lock()
	t := m.terms[id]
	m.mu.Unlock()
	if t != nil {
		m.request(t, reason, false)
	}
}

// CloseSession closes the terminal of the session, if any, and returns when it is gone.
func (m *Manager) CloseSession(sessionID, reason string) {
	m.closeWhere(reason, func(t *term) bool { return t.session == sessionID })
}

// CloseAll closes every terminal and returns when they are gone.
func (m *Manager) CloseAll(reason string) {
	m.closeWhere(reason, func(*term) bool { return true })
}

func (m *Manager) closeWhere(reason string, match func(*term) bool) {
	m.mu.Lock()
	var hit []*term
	for _, t := range m.terms {
		if match(t) {
			hit = append(hit, t)
		}
	}
	m.mu.Unlock()
	for _, t := range hit {
		m.request(t, reason, true)
	}
	for _, t := range hit {
		<-t.finished
	}
}

// request closes t, or marks it so that Open abandons it when it is still opening.
func (m *Manager) request(t *term, reason string, wait bool) {
	m.mu.Lock()
	if !t.started {
		if t.abort == "" {
			t.abort = reason
		}
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	if wait {
		t.terminate(reason)
	} else {
		go t.terminate(reason)
	}
}

func (t *term) terminate(reason string) {
	t.once.Do(func() { t.finish(reason) })
}

// input applies the limits of ADR 0026 (4 KiB per message, 64 KiB/s) and queues the bytes for the shell.
func (t *term) input(data []byte) {
	if len(data) == 0 {
		return
	}
	if len(data) > ChunkBytes {
		go t.terminate(ReasonInputFlood)
		return
	}
	now := t.m.cfg.Clock.Now()
	rate := float64(t.m.cfg.InputRate)
	t.mu.Lock()
	if el := now.Sub(t.refilled).Seconds(); el > 0 {
		t.tokens += el * rate
		if t.tokens > rate {
			t.tokens = rate
		}
		t.refilled = now
	}
	over := float64(len(data)) > t.tokens
	if !over {
		t.tokens -= float64(len(data))
		t.lastInput = now
	}
	t.mu.Unlock()
	if over {
		go t.terminate(ReasonInputFlood)
		return
	}
	select {
	case t.in <- append([]byte(nil), data...):
		t.bytesIn.Add(int64(len(data)))
	default: // the shell does not read: the queue is full
		go t.terminate(ReasonInputFlood)
	}
}

func (t *term) writeLoop() {
	for {
		select {
		case d := <-t.in:
			if _, err := t.ptmx.Write(d); err != nil {
				return
			}
		case <-t.quit:
			return
		}
	}
}

// readLoop reads the PTY only while the credit window has room, so a slow browser slows the shell down.
func (t *term) readLoop() {
	defer close(t.readerDone)
	window := t.m.cfg.Window
	buf := make([]byte, ChunkBytes)
	for {
		t.mu.Lock()
		for !t.closing && len(t.pending)+int(t.unacked) >= window {
			t.cond.Wait()
		}
		if t.closing {
			t.mu.Unlock()
			return
		}
		room := window - len(t.pending) - int(t.unacked)
		t.mu.Unlock()

		n, err := t.ptmx.Read(buf[:min(len(buf), room)])
		if n > 0 {
			t.mu.Lock()
			t.pending = append(t.pending, buf[:n]...)
			t.cond.Broadcast()
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// sendLoop sends the pending output in frames of at most ChunkBytes, at most one every FlushEvery.
func (t *term) sendLoop() {
	defer close(t.senderDone)
	select {
	case <-t.ready:
	case <-t.quit:
		return
	}
	for {
		t.mu.Lock()
		for len(t.pending) == 0 && !t.closing {
			t.cond.Wait()
		}
		if len(t.pending) == 0 || (t.closing && !t.flush) {
			t.mu.Unlock()
			return
		}
		n := min(len(t.pending), ChunkBytes)
		data := append([]byte(nil), t.pending[:n]...)
		t.pending = t.pending[n:]
		t.unacked += int64(n)
		seq := t.seq
		t.seq++
		t.cond.Broadcast()
		t.mu.Unlock()

		if t.m.cfg.Events.Output != nil {
			t.m.cfg.Events.Output(t.id, seq, data)
		}
		t.bytesOut.Add(int64(n))

		select {
		case <-time.After(t.m.cfg.FlushEvery):
		case <-t.quit:
		}
	}
}

// watch closes the terminal after IdleTimeout without input and after MaxAge.
func (t *term) watch() {
	cl := t.m.cfg.Clock
	idle := t.m.cfg.IdleTimeout
	age := cl.After(t.m.cfg.MaxAge)
	next := idle
	for {
		tick := cl.After(next)
		select {
		case <-t.quit:
			return
		case <-age:
			t.terminate(ReasonMaxAge)
			return
		case <-tick:
			t.mu.Lock()
			quiet := cl.Now().Sub(t.lastInput)
			t.mu.Unlock()
			if quiet >= idle {
				t.terminate(ReasonIdle)
				return
			}
			next = idle - quiet
		}
	}
}

// wait reaps the shell; its exit closes the terminal.
func (t *term) wait() {
	_ = t.cmd.Wait()
	if ps := t.cmd.ProcessState; ps != nil && ps.Exited() {
		c := ps.ExitCode()
		t.exitCode = &c
	}
	close(t.waitDone)
	t.terminate(ReasonExit)
}

// finish ends the terminal: flush (on exit) or drop the output, SIGHUP to the process group, SIGKILL after the
// grace to the group and to the descendants that left it, then the journal and the Closed event.
func (t *term) finish(reason string) {
	m := t.m
	<-t.ready
	if reason == ReasonExit {
		select {
		case <-t.readerDone:
		case <-time.After(drainWait):
		}
	}
	t.mu.Lock()
	t.closing, t.flush = true, reason == ReasonExit
	t.cond.Broadcast()
	t.mu.Unlock()
	close(t.quit)

	var below []identity
	if reason != ReasonExit {
		go func() { _, _ = io.Copy(io.Discard, t.ptmx) }() // a shell blocked on a full tty must see its SIGHUP
		below = descendants(t.pid)
		killGroup(t.pid, syscall.SIGHUP)
		select {
		case <-t.waitDone:
		default:
			_ = syscall.Kill(t.pid, syscall.SIGHUP)
		}
		select {
		case <-t.waitDone:
		case <-time.After(m.cfg.KillGrace):
		}
	}
	killGroup(t.pid, syscall.SIGKILL)
	select {
	case <-t.waitDone:
	default:
		_ = syscall.Kill(t.pid, syscall.SIGKILL)
	}
	killIdentities(below)
	select {
	case <-t.waitDone:
	case <-time.After(5 * time.Second):
	}
	_ = t.ptmx.Close()
	<-t.senderDone
	removeRecord(m.cfg.Home, t.id)

	m.mu.Lock()
	delete(m.terms, t.id)
	m.mu.Unlock()

	var code *int // only once wait has written it
	select {
	case <-t.waitDone:
		code = t.exitCode
	default:
	}
	if m.cfg.Events.Closed != nil {
		m.cfg.Events.Closed(t.id, reason, code)
	}
	exit := "none"
	if code != nil {
		exit = strconv.Itoa(*code)
	}
	m.cfg.Log("[terminal] closed session=%s terminal=%s device=%s reason=%s duration=%s bytes_in=%d bytes_out=%d exit_code=%s",
		t.session, t.id, m.cfg.DeviceID, reason, m.cfg.Clock.Now().Sub(t.opened).Round(time.Second),
		t.bytesIn.Load(), t.bytesOut.Load(), exit)
	close(t.finished)
}

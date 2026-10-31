package shim

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// File names inside a session directory.
const (
	// SocketName is the shim's Unix socket.
	SocketName = "shim.sock"
	// LockName is the lock file that makes one shim the owner of a session.
	LockName = "shim.lock"
)

// DefaultLinger is how long a shim waits, after its child exits, for a
// client to acknowledge the final record before it exits anyway.
const DefaultLinger = 24 * time.Hour

// Defaults for Config.DrainTimeout and Config.KillGrace.
const (
	DefaultDrainTimeout = 2 * time.Second
	DefaultKillGrace    = 5 * time.Second
)

// helloTimeout bounds how long a new connection may take to say hello.
const helloTimeout = 5 * time.Second

// maxSocketPath is the longest Unix socket path every supported platform
// accepts (macOS allows 104 bytes including the terminator).
const maxSocketPath = 103

// stdinQueueSize bounds the stdin lines waiting for the writer goroutine.
const stdinQueueSize = 64

// ErrLocked is returned by Run when another shim owns the session directory.
var ErrLocked = errors.New("another shim owns this session")

// Config describes the child a shim runs.
type Config struct {
	Dir     string   // session directory holding the spool, socket and lock
	WorkDir string   // the child's working directory; required
	Command []string // program and arguments
	Env     []string // complete child environment
	// Linger is how long to wait for an ack of the exit record after the
	// child exits; zero means DefaultLinger.
	Linger time.Duration
	// DrainTimeout is how long output may keep arriving after the child
	// exits (a descendant can hold the pipes open); zero means
	// DefaultDrainTimeout.
	DrainTimeout time.Duration
	// KillGrace is how long the child gets to obey SIGTERM, and then SIGKILL,
	// when ctx is cancelled; zero means DefaultKillGrace.
	KillGrace time.Duration
	// Logger receives lifecycle events; nil discards them.
	Logger *slog.Logger

	// wrapSpool, when set, wraps the spool's write side. Tests use it to
	// inject write failures.
	wrapSpool func(spoolFile) spoolFile
}

// SocketPath returns the shim's socket inside dir.
func SocketPath(dir string) string { return filepath.Join(dir, SocketName) }

// IgnoreParentSignals keeps SIGHUP, SIGINT and SIGPIPE from killing the shim
// when its terminal or parent goes away. It uses signal.Notify rather than
// signal.Ignore because ignored dispositions are inherited by the child.
func IgnoreParentSignals() {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGINT, syscall.SIGPIPE)
	go func() {
		for range ch {
		}
	}()
}

// stdinReq is one unit of work for the stdin writer goroutine.
type stdinReq struct {
	line  string
	close bool
}

// shim is one running shim.
type shim struct {
	cfg   Config
	log   *slog.Logger
	spool *spool
	cmd   *exec.Cmd
	pgid  int

	stdinFile    *os.File
	outR, errR   *os.File
	stdinQ       chan stdinReq
	pumps        sync.WaitGroup
	wg           sync.WaitGroup // accept, serve, stream and the stdin writer
	quit         chan struct{}  // closed when Run is shutting down
	exitCh       chan struct{}  // closed once the exit record is written
	waitDone     chan struct{}  // closed when the wait goroutine returns
	done         chan struct{}  // closed when the shim may exit
	doneOnce     sync.Once
	mu           sync.Mutex
	exited       bool // the child is gone and its output drained
	final        int64
	closing      bool
	conns        map[net.Conn]struct{}
	current      net.Conn // the client that owns the child, if any
	live         *conn    // current, with its write lock
	lingerTimer  *time.Timer
	recordFailed bool // spool appends fail; output is discarded
}

// Run starts the child and serves clients until a client acknowledges the
// exit record, the linger period after exit ends, or ctx is cancelled. On
// cancel the child's process group gets SIGTERM, then SIGKILL after
// KillGrace. Descendants still alive when Run returns are killed.
func Run(ctx context.Context, cfg Config) (err error) {
	if len(cfg.Command) == 0 {
		return errors.New("shim: no command")
	}
	if cfg.Dir == "" || cfg.WorkDir == "" {
		return errors.New("shim: Dir and WorkDir are required")
	}
	if cfg.Linger <= 0 {
		cfg.Linger = DefaultLinger
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = DefaultDrainTimeout
	}
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = DefaultKillGrace
	}
	lg := cfg.Logger
	if lg == nil {
		lg = slog.New(slog.DiscardHandler)
	}
	if n := len(SocketPath(cfg.Dir)); n > maxSocketPath {
		return fmt.Errorf("shim: socket path %q is %d bytes; the limit is %d", SocketPath(cfg.Dir), n, maxSocketPath)
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return fmt.Errorf("shim: create dir: %w", err)
	}
	lock, err := lockDir(cfg.Dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	sp, err := openSpool(SpoolPath(cfg.Dir))
	if err != nil {
		return err
	}
	if cfg.wrapSpool != nil {
		sp.w = cfg.wrapSpool(sp.w)
	}
	leaveSpoolOpen := false // set when goroutines may still write to it
	defer func() {
		if !leaveSpoolOpen {
			err = errors.Join(err, sp.close())
		}
	}()
	ln, err := listen(cfg.Dir)
	if err != nil {
		return err
	}
	defer func() {
		if rerr := os.Remove(SocketPath(cfg.Dir)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}()
	s := &shim{
		cfg: cfg, log: lg, spool: sp,
		stdinQ: make(chan stdinReq, stdinQueueSize),
		quit:   make(chan struct{}), exitCh: make(chan struct{}), waitDone: make(chan struct{}), done: make(chan struct{}),
		conns: make(map[net.Conn]struct{}),
	}
	if err := s.start(); err != nil {
		return errors.Join(err, ln.Close())
	}
	s.wg.Add(2)
	go s.accept(ln)
	go s.stdinLoop()

	var runErr error
	select {
	case <-s.done:
	case <-ctx.Done():
		lg.Info("shutting down", "reason", ctx.Err())
		runErr = s.terminate()
	}
	runErr = errors.Join(runErr, ln.Close())
	s.shutdown()
	if runErr != nil && !s.hasExited() {
		// The child could not be stopped. Free what its goroutines block on,
		// and keep the spool open if they do not finish.
		leaveSpoolOpen = !s.abandon()
	}
	s.wg.Wait()
	return runErr
}

// lockDir takes the exclusive, non-blocking lock that makes this process the
// session's only shim. The lock lives as long as the returned file is open.
func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Clean(filepath.Join(dir, LockName)), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("shim: open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		cerr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("shim: %w", ErrLocked), cerr)
		}
		return nil, errors.Join(fmt.Errorf("shim: lock %s: %w", dir, err), cerr)
	}
	return f, nil
}

// listen binds the socket under a temporary name and renames it into
// place, so a stale socket file is replaced atomically.
func listen(dir string) (net.Listener, error) {
	tmp := filepath.Join(dir, fmt.Sprintf(".s%d", os.Getpid()))
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("shim: remove temp socket: %w", err)
	}
	ln, err := net.Listen("unix", tmp)
	if err != nil {
		return nil, fmt.Errorf("shim: listen: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("shim: chmod socket: %w", err), ln.Close())
	}
	if err := os.Rename(tmp, SocketPath(dir)); err != nil {
		return nil, errors.Join(fmt.Errorf("shim: publish socket: %w", err), ln.Close())
	}
	return ln, nil
}

// start launches the child on pipes the shim owns and begins draining them.
func (s *shim) start() error {
	var ends []*os.File // every pipe end still open, closed on failure
	pipe := func() (r, w *os.File, err error) {
		r, w, err = os.Pipe()
		if err == nil {
			ends = append(ends, r, w)
		}
		return r, w, err
	}
	fail := func(err error) error {
		for _, f := range ends {
			_ = f.Close() // an end already closed returns an error we do not need
		}
		return err
	}
	inR, inW, err := pipe()
	if err != nil {
		return fail(fmt.Errorf("shim: stdin pipe: %w", err))
	}
	outR, outW, err := pipe()
	if err != nil {
		return fail(fmt.Errorf("shim: stdout pipe: %w", err))
	}
	errR, errW, err := pipe()
	if err != nil {
		return fail(fmt.Errorf("shim: stderr pipe: %w", err))
	}
	cmd := exec.Command(s.cfg.Command[0], s.cfg.Command[1:]...)
	cmd.Env = s.cfg.Env
	cmd.Dir = s.cfg.WorkDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("shim: start %s: %w", s.cfg.Command[0], err))
	}
	// The child holds its own copies; ours would keep the pipes open.
	for _, f := range []*os.File{inR, outW, errW} {
		_ = f.Close()
	}
	s.cmd, s.pgid = cmd, cmd.Process.Pid
	s.stdinFile, s.outR, s.errR = inW, outR, errR
	s.log.Info("child started", "pid", s.pgid, "command", s.cfg.Command[0])
	if _, err := s.spool.append(Record{Kind: KindStart, Pid: s.pgid}); err != nil {
		s.signal(syscall.SIGKILL)
		_ = cmd.Wait() // reap; the kill's outcome is all that matters
		return fail(err)
	}
	s.pumps.Add(2)
	go s.pump(outR, KindStdout)
	go s.pump(errR, KindStderr)
	go s.wait()
	return nil
}

// pump appends the child's output as records, at most ChunkSize of data each.
// It never stops draining the pipe: if the spool fails, output is discarded.
func (s *shim) pump(r *os.File, kind RecordKind) {
	defer s.pumps.Done()
	br := bufio.NewReaderSize(r, ChunkSize)
	continued := false // the last record had More set
	for {
		line, err := br.ReadSlice('\n')
		full := errors.Is(err, bufio.ErrBufferFull)
		data := line
		if !full && len(data) > 0 && data[len(data)-1] == '\n' {
			data = data[:len(data)-1]
		}
		if len(line) > 0 || (continued && err != nil) {
			s.record(kind, data, full)
			continued = full
		}
		if err != nil && !full {
			return
		}
	}
}

// record appends one output record, or drops it once the spool has failed.
func (s *shim) record(kind RecordKind, data []byte, more bool) {
	s.mu.Lock()
	failed := s.recordFailed
	s.mu.Unlock()
	if failed {
		return
	}
	rec := Record{Kind: kind, More: more}
	if utf8.Valid(data) {
		rec.Data = string(data)
	} else {
		rec.B = append([]byte(nil), data...)
	}
	if _, err := s.spool.append(rec); err != nil {
		s.log.Error("spool append failed; discarding further output", "error", err)
		s.mu.Lock()
		s.recordFailed = true
		s.mu.Unlock()
	}
}

// wait reaps the child, gives its output DrainTimeout to finish, and writes
// the exit record.
func (s *shim) wait() {
	defer close(s.waitDone)
	err := s.cmd.Wait()
	_ = s.stdinFile.Close() // unblocks a writer stuck on a child that never reads
	deadline := time.Now().Add(s.cfg.DrainTimeout)
	for _, f := range []*os.File{s.outR, s.errR} {
		if derr := f.SetReadDeadline(deadline); derr != nil {
			s.log.Error("set drain deadline", "error", derr)
		}
	}
	s.pumps.Wait()
	_ = s.outR.Close()
	_ = s.errR.Close()
	rec := exitRecord(err)
	s.mu.Lock()
	s.exited = true
	s.mu.Unlock()
	off, aerr := s.spool.append(rec)
	if aerr != nil {
		s.log.Error("exit record not fully written", "error", aerr)
		// off is 0 when nothing was written; only linger can end the shim then.
	}
	s.reapGroup() // the exit record is out; nothing the child left behind may outlive it
	s.log.Info("child exited", "code", rec.Code, "signal", rec.Signal)
	s.mu.Lock()
	s.final = off
	s.lingerTimer = time.AfterFunc(s.cfg.Linger, s.finish)
	s.mu.Unlock()
	close(s.exitCh)
}

// exitRecord describes how the child ended.
func exitRecord(err error) Record {
	rec := Record{Kind: KindExit}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			rec.Signal = signalName(ws.Signal())
		} else {
			rec.Code = ee.ExitCode()
		}
	default:
		rec.Code = -1
	}
	return rec
}

// signalName returns the protocol name of sig.
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "TERM"
	case syscall.SIGKILL:
		return "KILL"
	case syscall.SIGINT:
		return "INT"
	case syscall.SIGHUP:
		return "HUP"
	default:
		return fmt.Sprintf("SIG%d", int(sig))
	}
}

func (s *shim) finish() { s.doneOnce.Do(func() { close(s.done) }) }

func (s *shim) hasExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// signal sends sig to the child's whole process group.
func (s *shim) signal(sig syscall.Signal) {
	if s.pgid == 0 {
		return
	}
	_ = syscall.Kill(-s.pgid, sig) // the group may already be gone
}

// terminate stops the child: SIGTERM, then SIGKILL after KillGrace.
func (s *shim) terminate() error {
	if s.hasExited() {
		return nil
	}
	s.signal(syscall.SIGTERM)
	select {
	case <-s.exitCh:
		return nil
	case <-time.After(s.cfg.KillGrace):
	}
	s.log.Warn("child ignored SIGTERM; killing", "grace", s.cfg.KillGrace)
	s.signal(syscall.SIGKILL)
	select {
	case <-s.exitCh:
		return nil
	case <-time.After(s.cfg.KillGrace):
		return fmt.Errorf("shim: child %d did not exit after SIGKILL", s.pgid)
	}
}

// reapGroup kills whatever is left of the child's process group.
func (s *shim) reapGroup() {
	if s.pgid == 0 {
		return
	}
	if syscall.Kill(-s.pgid, 0) == nil {
		s.log.Warn("killing leftover descendants", "pgid", s.pgid)
		s.signal(syscall.SIGKILL)
	}
}

// abandon is the last resort for a child that cannot be stopped: it closes
// the pipes the shim holds so blocked goroutines return, then waits up to
// KillGrace for them. It reports whether they all finished, which is when
// the spool is safe to close.
func (s *shim) abandon() bool {
	for _, f := range []*os.File{s.stdinFile, s.outR, s.errR} {
		if f != nil {
			_ = f.Close() // unblocks the goroutine using it
		}
	}
	finished := make(chan struct{})
	go func() {
		s.pumps.Wait()
		<-s.waitDone
		close(finished)
	}()
	select {
	case <-finished:
		return true
	case <-time.After(s.cfg.KillGrace):
		s.log.Error("goroutines still running after abandoning the child; leaving the spool open")
		return false
	}
}

// shutdown refuses new clients, drops the connected ones and stops the timer.
func (s *shim) shutdown() {
	s.mu.Lock()
	s.closing = true
	for c := range s.conns {
		_ = c.Close() // the serve goroutine notices and exits
	}
	if s.lingerTimer != nil {
		s.lingerTimer.Stop()
	}
	s.mu.Unlock()
	close(s.quit)
}

// accept hands each connection to its own goroutine.
func (s *shim) accept(ln net.Listener) {
	defer s.wg.Done()
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			_ = nc.Close()
			return
		}
		s.conns[nc] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serve(nc)
	}
}

package shim

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// SocketName is the shim's Unix socket inside a session directory.
const SocketName = "shim.sock"

// DefaultLinger is how long a shim waits, after its child exits, for a
// client to acknowledge the final record before it exits anyway.
const DefaultLinger = 24 * time.Hour

// helloTimeout bounds how long a new connection may take to say hello.
const helloTimeout = 5 * time.Second

// Config describes the child a shim runs.
type Config struct {
	Dir     string   // session directory holding the spool and the socket
	Command []string // program and arguments
	Env     []string // complete child environment
	Linger  time.Duration
}

// SocketPath returns the shim's socket inside dir.
func SocketPath(dir string) string { return filepath.Join(dir, SocketName) }

// shim is one running shim.
type shim struct {
	cfg   Config
	spool *spool
	cmd   *exec.Cmd

	stdinMu sync.Mutex
	stdin   io.WriteCloser

	mu       sync.Mutex
	exited   bool
	final    int64         // spool offset after the exit record
	current  net.Conn      // the one connected client, if any
	exitCh   chan struct{} // closed once the exit record is written
	done     chan struct{}
	doneOnce sync.Once
}

// Run starts the child and serves clients until a client acknowledges the
// exit record, the linger period after exit ends, or ctx is cancelled (which
// terminates the child's process group first).
func Run(ctx context.Context, cfg Config) (err error) {
	if len(cfg.Command) == 0 {
		return errors.New("shim: no command")
	}
	if cfg.Linger <= 0 {
		cfg.Linger = DefaultLinger
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return fmt.Errorf("shim: create dir: %w", err)
	}
	sp, err := openSpool(SpoolPath(cfg.Dir))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sp.close()) }()
	ln, err := listen(cfg.Dir)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, ln.Close())
		if rerr := os.Remove(SocketPath(cfg.Dir)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}()
	s := &shim{cfg: cfg, spool: sp, exitCh: make(chan struct{}), done: make(chan struct{})}
	if err := s.start(); err != nil {
		return err
	}
	go s.accept(ln)
	select {
	case <-s.done:
	case <-ctx.Done():
		s.signal(syscall.SIGTERM)
		<-s.exitCh
	}
	s.mu.Lock()
	if s.current != nil {
		s.current.Close()
	}
	s.mu.Unlock()
	return nil
}

// listen binds the socket under a temporary name and renames it into
// place, so a stale socket file is replaced atomically.
func listen(dir string) (net.Listener, error) {
	tmp := filepath.Join(dir, fmt.Sprintf(".shim-%d.sock", os.Getpid()))
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("shim: remove temp socket: %w", err)
	}
	ln, err := net.Listen("unix", tmp)
	if err != nil {
		return nil, fmt.Errorf("shim: listen: %w", err)
	}
	if err := os.Rename(tmp, SocketPath(dir)); err != nil {
		return nil, errors.Join(fmt.Errorf("shim: publish socket: %w", err), ln.Close())
	}
	return ln, nil
}

func (s *shim) start() error {
	cmd := exec.Command(s.cfg.Command[0], s.cfg.Command[1:]...)
	cmd.Env = s.cfg.Env
	cmd.Dir = s.cfg.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("shim: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("shim: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("shim: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("shim: start %s: %w", s.cfg.Command[0], err)
	}
	s.cmd, s.stdin = cmd, stdin
	if _, err := s.spool.append(Record{Kind: KindStart, Pid: cmd.Process.Pid}); err != nil {
		return err
	}
	var pumps sync.WaitGroup
	pumps.Add(2)
	go s.pump(&pumps, stdout, KindStdout)
	go s.pump(&pumps, stderr, KindStderr)
	go s.wait(&pumps)
	return nil
}

// pump appends each line the child writes as a record.
func (s *shim) pump(wg *sync.WaitGroup, r io.Reader, kind RecordKind) {
	defer wg.Done()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if _, aerr := s.spool.append(Record{Kind: kind, Data: line}); aerr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// wait records the child's exit after its output is drained.
func (s *shim) wait(pumps *sync.WaitGroup) {
	pumps.Wait()
	err := s.cmd.Wait()
	rec := Record{Kind: KindExit}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			rec.Signal = ws.Signal().String()
		} else {
			rec.Code = ee.ExitCode()
		}
	} else if err != nil {
		rec.Code = -1
	}
	off, aerr := s.spool.append(rec)
	s.mu.Lock()
	s.exited = true
	s.final = off
	if aerr != nil {
		s.final = 0 // the exit record is missing; only linger can end the shim
	}
	s.mu.Unlock()
	close(s.exitCh)
	time.AfterFunc(s.cfg.Linger, s.finish)
}

func (s *shim) finish() { s.doneOnce.Do(func() { close(s.done) }) }

func (s *shim) signal(sig syscall.Signal) {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-s.cmd.Process.Pid, sig) // the group may already be gone
}

// accept serves one client at a time; a new connection replaces the old.
func (s *shim) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.current != nil {
			s.current.Close()
		}
		s.current = conn
		s.mu.Unlock()
		go s.serve(conn)
	}
}

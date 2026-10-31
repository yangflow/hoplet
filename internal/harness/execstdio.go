package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	// drainBound bounds how long a descendant holding an output pipe can
	// delay the Exit line after the process itself has exited: once the
	// process is gone, each read of its output may wait this long for data.
	// Time spent blocked delivering a line to a slow reader never counts.
	drainBound = 2 * time.Second
	// closeBound bounds how long Close waits for the goroutines to stop.
	closeBound = 5 * time.Second
)

// ExecStdio runs a process directly and exposes its stdio as Lines. It is
// for probes and tests; sessions run through a shim instead. It cannot
// outlive its process: Close terminates the process group.
type ExecStdio struct {
	cmd   *exec.Cmd
	stdin *os.File
	outs  []*os.File // read ends of stdout and stderr
	lines chan Line
	wmu   sync.Mutex // serializes WriteLine

	done      chan struct{} // closed by Close
	finished  chan struct{} // closed when the waiter has stopped
	closeOnce sync.Once
	readers   sync.WaitGroup

	smu    sync.Mutex // guards exited and group signals
	exited bool

	werr error // set under wmu once a partial write has poisoned stdin
}

func (e *ExecStdio) hasExited() bool {
	e.smu.Lock()
	defer e.smu.Unlock()
	return e.exited
}

// outReader reads a child's output pipe. After the process has exited every
// read gets a fresh deadline, so a descendant that keeps the pipe open cannot
// stall the reader for longer than drainBound.
type outReader struct {
	e *ExecStdio
	f *os.File
}

func (r outReader) Read(p []byte) (int, error) {
	if r.e.hasExited() {
		_ = r.f.SetReadDeadline(time.Now().Add(drainBound)) // fails only if f is closed; Read reports that
	}
	return r.f.Read(p)
}

// StartExec starts command in dir with env. A nil env means an empty
// environment, never the caller's.
func StartExec(ctx context.Context, command []string, env []string, dir string) (*ExecStdio, error) {
	if len(command) == 0 {
		return nil, errors.New("harness: no command")
	}
	if env == nil {
		env = []string{}
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stdin pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)
		return nil, fmt.Errorf("harness: stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, fmt.Errorf("harness: stderr pipe: %w", err)
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env, cmd.Dir = env, dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	err = cmd.Start()
	closeAll(inR, outW, errW) // the child has its own copies
	if err != nil {
		closeAll(inW, outR, errR)
		return nil, fmt.Errorf("harness: start %s: %w", command[0], err)
	}
	e := &ExecStdio{
		cmd:      cmd,
		stdin:    inW,
		outs:     []*os.File{outR, errR},
		lines:    make(chan Line, 64),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	e.readers.Add(2)
	go e.read(outR, Stdout)
	go e.read(errR, Stderr)
	go e.wait()
	return e, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close() // best effort on cleanup paths
	}
}

// send delivers l unless Close has been called.
func (e *ExecStdio) send(l Line) bool {
	select {
	case e.lines <- l:
		return true
	case <-e.done:
		return false
	}
}

// read turns r into Lines. A line longer than MaxLine arrives as
// consecutive Lines of at most MaxLine bytes. Only the trailing '\n' is
// stripped, so CRLF stays byte-exact.
func (e *ExecStdio) read(r *os.File, kind LineKind) {
	defer e.readers.Done()
	br := bufio.NewReaderSize(outReader{e: e, f: r}, 64<<10)
	var pending []byte
	for {
		chunk, err := br.ReadSlice('\n')
		complete := err == nil && len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if complete {
			chunk = chunk[:len(chunk)-1]
		}
		pending = append(pending, chunk...)
		for len(pending) > MaxLine {
			piece := append([]byte(nil), pending[:MaxLine]...)
			pending = append([]byte(nil), pending[MaxLine:]...)
			if !e.send(Line{Kind: kind, Data: piece}) {
				return
			}
		}
		if complete {
			if !e.send(Line{Kind: kind, Data: append([]byte(nil), pending...)}) {
				return
			}
			pending = pending[:0]
			continue
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			if len(pending) > 0 {
				e.send(Line{Kind: kind, Data: append([]byte(nil), pending...)})
			}
			return
		}
	}
}

// wait reaps the process, drains the readers (bounded) and delivers the
// Exit line.
func (e *ExecStdio) wait() {
	defer close(e.finished)
	defer close(e.lines)
	waitErr := e.cmd.Wait()
	e.smu.Lock()
	e.exited = true
	e.smu.Unlock()
	// A read already blocked when the process exited gets its deadline now.
	for _, f := range e.outs {
		_ = f.SetReadDeadline(time.Now().Add(drainBound)) // fails only if f is closed
	}

	drained := make(chan struct{})
	go func() {
		e.readers.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-e.done:
		closeAll(e.outs...)
		<-drained
	}
	closeAll(e.outs...)

	code, sig := exitInfo(e.cmd.ProcessState)
	if e.cmd.ProcessState == nil && waitErr != nil {
		code = -1
	}
	e.send(Line{Kind: Exit, Code: code, Signal: sig})
}

func exitInfo(ps *os.ProcessState) (int, string) {
	if ps == nil {
		return -1, ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -1, signalName(ws.Signal())
	}
	return ps.ExitCode(), ""
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "INT"
	case syscall.SIGTERM:
		return "TERM"
	case syscall.SIGKILL:
		return "KILL"
	case syscall.SIGHUP:
		return "HUP"
	}
	return fmt.Sprintf("SIG%d", int(s))
}

// WriteLine writes line plus a newline to the process's stdin. It honors
// ctx: when ctx is cancelled or its deadline passes while the child is not
// reading, WriteLine returns ctx's error (part of the line may have been
// written).
func (e *ExecStdio) WriteLine(ctx context.Context, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.wmu.Lock()
	defer e.wmu.Unlock()
	if e.werr != nil {
		return e.werr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = e.stdin.SetWriteDeadline(dl) // fails only if stdin is closed; the write reports that
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(fired)
		_ = e.stdin.SetWriteDeadline(time.Now()) // fails only if stdin is closed
	})
	buf := append(append([]byte(nil), line...), '\n')
	n, err := e.stdin.Write(buf)
	if !stop() {
		<-fired
	}
	_ = e.stdin.SetWriteDeadline(time.Time{}) // fails only if stdin is closed
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		} else if errors.Is(err, os.ErrDeadlineExceeded) {
			err = context.DeadlineExceeded // ctx's deadline fired before ctx.Err reported it
		}
		if n > 0 && n < len(buf) {
			// Part of the line is in the pipe; anything written now would
			// splice into it, so stdin is unusable from here on.
			e.werr = fmt.Errorf("harness: partial stdin write (%d of %d bytes), stdin is unusable: %w", n, len(buf), err)
			return e.werr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("harness: write stdin: %w", err)
	}
	return nil
}

// Lines delivers output lines, then the Exit line, then closes.
func (e *ExecStdio) Lines() <-chan Line { return e.lines }

// Signal sends INT, TERM, HUP or KILL to the process group. It fails once
// the process has exited.
func (e *ExecStdio) Signal(name string) error {
	var sig syscall.Signal
	switch name {
	case "INT":
		sig = syscall.SIGINT
	case "TERM":
		sig = syscall.SIGTERM
	case "HUP":
		sig = syscall.SIGHUP
	case "KILL":
		sig = syscall.SIGKILL
	default:
		return fmt.Errorf("harness: unknown signal %q", name)
	}
	return e.signal(sig)
}

func (e *ExecStdio) signal(sig syscall.Signal) error {
	e.smu.Lock()
	defer e.smu.Unlock()
	if e.exited {
		return errors.New("harness: process has exited")
	}
	if err := syscall.Kill(-e.cmd.Process.Pid, sig); err != nil {
		return fmt.Errorf("harness: signal process group: %w", err)
	}
	return nil
}

// killGroup kills every remaining member of the process group. Once the
// leader has exited the group is probed first: while any member lives the
// group ID cannot have been reused.
func (e *ExecStdio) killGroup() {
	e.smu.Lock()
	defer e.smu.Unlock()
	pgid := -e.cmd.Process.Pid
	if e.exited && syscall.Kill(pgid, 0) != nil {
		return
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL) // members may have just exited
}

// Close closes stdin, kills the process group and waits (bounded) for the
// reader and waiter goroutines to stop. It is safe to call more than once
// and does not need Lines to be read.
func (e *ExecStdio) Close() error {
	var err error
	e.closeOnce.Do(func() {
		if cerr := e.stdin.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			err = cerr
		}
		e.killGroup()
		close(e.done)
	})
	select {
	case <-e.finished:
	case <-time.After(closeBound):
		return errors.New("harness: process did not stop in time")
	}
	return err
}

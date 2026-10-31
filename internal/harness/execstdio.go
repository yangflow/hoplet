package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// ExecStdio runs a process directly and exposes its stdio as Lines. It is
// for probes and tests; sessions run through a shim instead.
type ExecStdio struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan Line
	wmu   sync.Mutex
}

// StartExec starts command in dir with env.
func StartExec(ctx context.Context, command []string, env []string, dir string) (*ExecStdio, error) {
	if len(command) == 0 {
		return nil, errors.New("harness: no command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env, cmd.Dir = env, dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("harness: start %s: %w", command[0], err)
	}
	e := &ExecStdio{cmd: cmd, stdin: stdin, lines: make(chan Line, 64)}
	var wg sync.WaitGroup
	wg.Add(2)
	go e.read(&wg, stdout, Stdout)
	go e.read(&wg, stderr, Stderr)
	go func() {
		wg.Wait()
		code := -1
		var ee *exec.ExitError
		if err := cmd.Wait(); err == nil {
			code = 0
		} else if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		e.lines <- Line{Kind: Exit, Code: code}
		close(e.lines)
	}()
	return e, nil
}

func (e *ExecStdio) read(wg *sync.WaitGroup, r io.Reader, kind LineKind) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		e.lines <- Line{Kind: kind, Data: append([]byte(nil), sc.Bytes()...)}
	}
}

// WriteLine writes line plus a newline to the process's stdin.
func (e *ExecStdio) WriteLine(line []byte) error {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	if _, err := e.stdin.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		return fmt.Errorf("harness: write stdin: %w", err)
	}
	return nil
}

// Lines delivers output lines, then the Exit line, then closes.
func (e *ExecStdio) Lines() <-chan Line { return e.lines }

// Close closes stdin and kills the process group.
func (e *ExecStdio) Close() error {
	e.wmu.Lock()
	err := e.stdin.Close()
	e.wmu.Unlock()
	if e.cmd.Process != nil {
		_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGKILL) // may already be gone
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
)

// Spawner starts a shim for a session.
type Spawner interface {
	Spawn(ctx context.Context, dir, workDir string, l harness.Launch) error
}

// ExecSpawner runs `<Exe> shim` as a detached process: its own session
// (setsid), stdin from /dev/null, stdout and stderr appended to
// <dir>/shim.log, and the launch environment as its whole environment, so
// it survives the daemon and inherits nothing else.
type ExecSpawner struct {
	Exe string
}

// Spawn starts the shim and returns once it is started; use dialWhenReady to wait
// until it accepts connections.
func (e ExecSpawner) Spawn(_ context.Context, dir, workDir string, l harness.Launch) error {
	if len(l.Command) == 0 {
		return errors.New("session: launch has no command")
	}
	logf, err := os.OpenFile(filepath.Join(filepath.Clean(dir), "shim.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("session: open shim log: %w", err)
	}
	defer logf.Close()
	args := append([]string{"shim", "--dir", dir, "--cwd", workDir, "--"}, l.Command...)
	cmd := exec.Command(e.Exe, args...)
	cmd.Env = l.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("session: start shim: %w", err)
	}
	go func() { _ = cmd.Wait() }() // reap; the shim's exit status lives in its spool
	return nil
}

// dialWhenReady dials the shim in dir, retrying until it accepts
// connections or ctx ends; a shim is ready once it answers with welcome.
func dialWhenReady(ctx context.Context, dir string, offset int64) (*shim.Client, error) {
	delay := 10 * time.Millisecond
	for {
		c, err := shim.Dial(ctx, dir, offset)
		if err == nil {
			return c, nil
		}
		var serr *shim.Error
		if errors.As(err, &serr) {
			return nil, err // the shim answered: retrying won't help
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("session: shim in %s not ready: %w", dir, errors.Join(ctx.Err(), err))
		case <-time.After(delay):
		}
		delay = min(2*delay, 200*time.Millisecond)
	}
}

// shimLogTail returns the last lines of the shim's log in dir, formatted to
// follow an error message; empty when there is nothing to show.
func shimLogTail(dir string) string {
	b, err := os.ReadFile(filepath.Join(filepath.Clean(dir), "shim.log"))
	if err != nil || len(b) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return "\nshim.log:\n" + strings.Join(lines, "\n")
}

// Package claude is the runtime driver for Claude Code (ADR 0008): one process in streaming JSON mode per
// session, controlled through Claude Code's stdio control protocol.
package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// Options configures the driver.
type Options struct {
	// Bin is the Claude Code executable; empty means `claude` on PATH.
	Bin string
	// Model selects the model with --model; empty leaves Claude Code's default.
	Model string
}

// Driver starts Claude Code processes.
type Driver struct {
	bin   string
	model string

	mu          sync.Mutex
	versionSeen bool
	version     string
	tested      bool
}

func New(opts Options) *Driver {
	bin := opts.Bin
	if bin == "" {
		bin = "claude"
	}
	return &Driver{bin: bin, model: opts.Model}
}

func (d *Driver) Name() string { return "claude" }

func (d *Driver) Capabilities() []runtime.Capability {
	return []runtime.Capability{
		runtime.CapPermissions, runtime.CapInterrupt, runtime.CapThinking, runtime.CapFast, runtime.CapModels,
		runtime.CapImages, runtime.CapSetModel,
	}
}

// models are the aliases `claude --help` documents for --model; Claude Code resolves each to its latest model.
var models = []string{"fable", "opus", "sonnet"}

func (d *Driver) Models() []string { return append([]string(nil), models...) }

// Args are the command line of a process for spec. Options map to Claude Code's own: --model, --thinking
// (enabled or disabled) and fast mode through the settings of this one process (--settings
// '{"fastMode":true}'). A new session gets --session-id; an existing one is resumed.
func (d *Driver) Args(spec runtime.StartSpec) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
	}
	model := d.model
	if spec.Options.Model != "" {
		model = spec.Options.Model
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if t := spec.Options.Thinking; t != nil {
		mode := "disabled"
		if *t {
			mode = "enabled"
		}
		args = append(args, "--thinking", mode)
	}
	if spec.Options.Fast {
		args = append(args, "--settings", `{"fastMode":true}`)
	}
	if spec.New {
		return append(args, "--session-id", spec.SessionID)
	}
	return append(args, "--resume", spec.SessionID)
}

// checkVersion runs `claude --version` until a supported version was seen once.
func (d *Driver) checkVersion(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.versionSeen {
		return nil
	}
	out, err := exec.CommandContext(ctx, d.bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("run %s --version: %w", d.bin, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return errors.New("claude --version printed nothing")
	}
	tested, err := CheckVersion(fields[0])
	if err != nil {
		return err
	}
	d.versionSeen, d.version, d.tested = true, fields[0], tested
	return nil
}

// Version is the installed Claude Code version and whether it is one the driver was tested against; it
// fails when the version cannot be read or is refused.
func (d *Driver) Version(ctx context.Context) (string, bool, error) {
	if err := d.checkVersion(ctx); err != nil {
		return "", false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version, d.tested, nil
}

func (d *Driver) Start(ctx context.Context, spec runtime.StartSpec) (runtime.Process, error) {
	if err := d.checkVersion(ctx); err != nil {
		return nil, err
	}
	cmd := exec.Command(d.bin, d.Args(spec)...)
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", d.bin, err)
	}
	p := &process{
		cmd:     cmd,
		stdin:   stdin,
		events:  make(chan runtime.ProcessEvent, 256),
		stderr:  &stderr,
		pending: map[string]pendingRequest{},
	}
	go p.read(stdout)
	if err := p.send(controlRequest(initRequestID, "initialize")); err != nil {
		_ = p.Kill()
		return nil, err
	}
	return p, nil
}

// tailBuffer keeps the last bytes written to it, for error messages.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailLimit = 4 << 10

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailLimit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailLimit:]...)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}

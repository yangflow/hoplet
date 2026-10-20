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
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// Options configures the driver.
type Options struct {
	// Bin is the Claude Code executable; empty means `claude` on PATH.
	Bin string
	// Model selects the model with --model; empty leaves Claude Code's default.
	Model string
	// Env is the environment of every process the driver starts, `claude --version` included; nil means the
	// agent's own.
	Env []string
	// Defaults replace the defaults of a new session's options, by option ID; a value the option does not
	// declare is ignored.
	Defaults map[string]any
}

// Driver starts Claude Code processes.
type Driver struct {
	bin      string
	model    string
	env      []string
	defaults map[string]any

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
	return &Driver{bin: bin, model: opts.Model, env: opts.Env, defaults: opts.Defaults}
}

func (d *Driver) Name() string { return "claude" }

func (d *Driver) Capabilities() []runtime.Capability {
	return []runtime.Capability{runtime.CapPermissions, runtime.CapInterrupt, runtime.CapImages, runtime.CapSteer}
}

// Option IDs and the value that leaves the model to Claude Code (ADR 0018).
const (
	optMode      = "mode"
	optModel     = "model"
	optThinking  = "thinking"
	optFast      = "fast"
	modelDefault = "default"
	modeDefault  = "default"
	modeBypass   = "bypassPermissions"
)

// Config declares the permission mode, the model, Thinking and Fast, in that order. The models are the aliases `claude --help`
// documents for --model; Claude Code resolves each to its latest model, and `default` leaves the choice to it.
func (d *Driver) Config() []runtime.ConfigOption {
	return runtime.ApplyDefaults(d.options(), d.defaults)
}

func (d *Driver) options() []runtime.ConfigOption {
	return []runtime.ConfigOption{
		{
			// Claude Code's own modes except dontAsk, which it lists last and `claude --help` leaves out.
			ID: optMode, Name: "Permission mode", Category: "mode", Type: runtime.ConfigSelect, Current: modeDefault,
			Apply: runtime.ApplyNow,
			Options: []runtime.ConfigValue{
				{Value: modeDefault, Name: "Manual", Description: "Ask before edits and commands.", Icon: "shield"},
				{Value: "acceptEdits", Name: "Accept edits", Description: "Edit files without asking; ask before commands.", Icon: "file-pen"},
				{Value: "plan", Name: "Plan", Description: "Explore and plan; no edits or commands.", Icon: "list-checks"},
				{Value: "auto", Name: "Auto", Description: "Claude decides when to ask, with safety checks.", Icon: "sparkles"},
				{Value: modeBypass, Name: "Bypass permissions", Description: "Run everything without asking.", Risk: "high", Icon: "shield-alert"},
			},
		},
		{
			ID: optModel, Name: "Model", Category: "model", Type: runtime.ConfigSelect, Current: modelDefault,
			Apply: runtime.ApplyNextTurn,
			Options: []runtime.ConfigValue{
				{Value: modelDefault, Name: "Default model"},
				{Value: "fable", Name: "fable"},
				{Value: "opus", Name: "opus"},
				{Value: "sonnet", Name: "sonnet"},
			},
		},
		{
			ID: optThinking, Name: "Thinking", Category: "thought_level", Type: runtime.ConfigBoolean, Current: true,
			Apply: runtime.ApplyRestart,
		},
		{
			ID: optFast, Name: "Fast", Category: "model_config", Type: runtime.ConfigBoolean, Current: false,
			Apply: runtime.ApplyRestart,
		},
	}
}

// Args are the command line of a process for spec. The session's values map to Claude Code's own flags:
// --permission-mode (with --allow-dangerously-skip-permissions on every process, so that Bypass can be switched
// to later; Claude Code refuses that switch otherwise), --model, --thinking (enabled or disabled) and fast mode through the settings of this one process (--settings
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
	mode := modeDefault
	if m, _ := spec.Config[optMode].(string); m != "" {
		mode = m
	}
	args = append(args, "--permission-mode", mode, "--allow-dangerously-skip-permissions")
	model := d.model
	if m, _ := spec.Config[optModel].(string); m != "" && m != modelDefault {
		model = m
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	thinking := "enabled"
	if on, ok := spec.Config[optThinking].(bool); ok && !on {
		thinking = "disabled"
	}
	args = append(args, "--thinking", thinking)
	if on, _ := spec.Config[optFast].(bool); on {
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
	out, err := d.versionCmd(ctx).Output()
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
	cmd.Env = processEnv(d.baseEnv())
	p, err := launch(cmd, d.model)
	if err != nil {
		return nil, err
	}
	if err := p.initialize(); err != nil {
		return nil, err
	}
	return p, nil
}

// launch starts cmd and reads its output.
func launch(cmd *exec.Cmd, model string) (*process, error) {
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
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	p := &process{
		cmd:     cmd,
		stdin:   stdin,
		events:  make(chan runtime.ProcessEvent, 256),
		stderr:  &stderr,
		pending: map[string]pendingRequest{},
		waiting: map[string]chan controlReply{},
		done:    make(chan struct{}),
		model:   model,
	}
	go p.read(stdout)
	return p, nil
}

// exitGrace is how long initialize waits for a process whose input broke to report why it ended.
const exitGrace = 5 * time.Second

// initialize sends the initialize request. A process that already exited has closed its input, so the write
// fails with a broken pipe whatever the cause; the reason the user needs is the exit, which is reported instead.
func (p *process) initialize() error {
	err := p.send(controlRequest(initRequestID, "initialize"))
	if err == nil {
		return nil
	}
	select {
	case <-p.done:
	case <-time.After(exitGrace):
		_ = p.Kill()
		return err
	}
	for ev := range p.events {
		if ex, ok := ev.(runtime.Exited); ok && ex.Err != nil {
			return fmt.Errorf("the process exited: %w", ex.Err)
		}
	}
	return fmt.Errorf("the process exited: %w", err)
}

// stateEventsEnv makes Claude Code report session_state_changed, which is how the agent learns that a turn
// ended or waits for a permission; without it Claude Code stays silent and the session never becomes ready.
const stateEventsEnv = "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"

// baseEnv is the environment the driver's processes start from.
func (d *Driver) baseEnv() []string {
	if d.env != nil {
		return d.env
	}
	return os.Environ()
}

func (d *Driver) versionCmd(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, d.bin, "--version")
	cmd.Env = d.baseEnv()
	return cmd
}

// processEnv is env with the session state events switched on, whatever the inherited value was.
func processEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, stateEventsEnv+"=") {
			out = append(out, kv)
		}
	}
	return append(out, stateEventsEnv+"=1")
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

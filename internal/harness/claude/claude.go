// Package claude drives Claude Code through its stream-json protocol:
// `claude -p --input-format stream-json --output-format stream-json`, with
// permission prompts routed to Hoplet over the same stdio
// (`--permission-prompt-tool stdio`) as control requests.
//
// Hoplet runs its own copy of claude (see Install), pinned to a version
// this adapter is verified against; logins stay shared through ~/.claude.
package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// Kind is the harness kind.
const Kind = "claude"

// probeTimeout bounds `claude --version`.
const probeTimeout = 10 * time.Second

// verifiedVersions are the claude versions this adapter's contract tests
// cover. Add a version only together with its recordings in testdata.
// Other versions run once they pass SelfCheck on the user's machine (see
// Install).
func verifiedVersions() []string { return []string{"2.1.312"} }

func verified(v string) bool { return slices.Contains(verifiedVersions(), v) }

// BinaryPath is the managed claude binary in Hoplet's home.
func BinaryPath(home string) string { return filepath.Join(home, "harness", Kind, "bin", "claude") }

// Harness is Claude Code.
type Harness struct {
	// Binary is the claude executable Hoplet runs (BinaryPath).
	Binary string

	mu     sync.Mutex
	cached harness.Info
	stamp  stamp // the binary the cached info is for
}

// stamp identifies a binary's contents cheaply: its resolved path, size
// and modification time.
type stamp struct {
	path string
	size int64
	mod  time.Time
}

var _ harness.StdioHarness = (*Harness)(nil)

// Kind reports "claude".
func (h *Harness) Kind() string { return Kind }

// caps is what this adapter supports. Steering, questions and fast mode
// are not verified yet (claude 2.1.312 keeps fast mode off through its
// stdio protocol), and claude has no ultra effort.
func caps() proto.Capabilities {
	return proto.Capabilities{
		Resume: true, SetMode: true, SetModel: true, PlanMode: true, PartialStream: true,
		Modes:   []proto.PermissionMode{proto.ModeConfirm, proto.ModeAutoEdit, proto.ModeAutoReview, proto.ModeFullAuto},
		Efforts: []proto.Effort{proto.EffortLow, proto.EffortMedium, proto.EffortHigh, proto.EffortXHigh, proto.EffortMax},
	}
}

// ErrNotInstalled says Hoplet's copy of claude is missing.
var ErrNotInstalled = errors.New("claude: Hoplet's copy of claude is not installed (run `hoplet harness install claude`)")

// Probe runs `claude --version`, once per installed binary: the daemon
// probes on every session creation and setting change.
func (h *Harness) Probe(ctx context.Context) (harness.Info, error) {
	st, err := binaryStamp(h.Binary)
	if err != nil {
		return harness.Info{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if st != h.stamp {
		v, err := version(ctx, h.Binary)
		if err != nil {
			return harness.Info{}, err
		}
		h.cached = harness.Info{Version: v, Capabilities: caps(), Supported: verified(v) || localVerified(h.Binary, v)}
		h.stamp = st
	}
	info := h.cached
	// Not cached: a failed follow-up leaves the binary as it was.
	if u, ok := lastUpdate(h.Binary); ok {
		info.Notice, info.Attention = u.notice(info.Version)
	}
	return info, nil
}

func binaryStamp(bin string) (stamp, error) {
	path, err := filepath.EvalSymlinks(bin)
	if errors.Is(err, os.ErrNotExist) {
		return stamp{}, ErrNotInstalled
	}
	if err != nil {
		return stamp{}, fmt.Errorf("claude: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return stamp{}, fmt.Errorf("claude: %w", err)
	}
	return stamp{path: path, size: fi.Size(), mod: fi.ModTime()}, nil
}

// version runs bin --version and parses "2.1.312 (Claude Code)". The probe
// runs outside the harness environment filter, in the daemon's or CLI's
// own process, so it gets only PATH (a launcher may need an interpreter
// found there), HOME and the updater switch instead of the caller's
// environment: no credential variables reach a binary that has not passed
// the self-check yet, and claude's updater stays off.
func version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "DISABLE_AUTOUPDATER=1"}
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude: %s --version: %w", bin, err)
	}
	return parseVersion(out)
}

func parseVersion(out []byte) (string, error) {
	fields := strings.Fields(string(out))
	if len(fields) == 0 || !strings.Contains(string(out), "Claude Code") {
		return "", fmt.Errorf("claude: unexpected --version output %q", strings.TrimSpace(string(out)))
	}
	return fields[0], nil
}

// planMode is claude's plan mode, which Hoplet keeps as a separate toggle.
const planMode = "plan"

// nativeMode maps Hoplet's permission mode to claude's.
func nativeMode(m proto.PermissionMode) (string, error) {
	switch m {
	case proto.ModeConfirm, "":
		return "default", nil
	case proto.ModeAutoEdit:
		return "acceptEdits", nil
	case proto.ModeAutoReview:
		return "auto", nil
	case proto.ModeFullAuto:
		return "bypassPermissions", nil
	}
	return "", fmt.Errorf("claude: unknown permission mode %q", m)
}

// hopletMode maps a claude permission mode other than plan back to
// Hoplet's; modes Hoplet has no level for count as confirm, the strictest.
func hopletMode(native string) proto.PermissionMode {
	switch native {
	case "acceptEdits":
		return proto.ModeAutoEdit
	case "auto":
		return proto.ModeAutoReview
	case "bypassPermissions":
		return proto.ModeFullAuto
	}
	return proto.ModeConfirm
}

// Launch returns the claude command line. The environment is the session's
// minus the variables that make claude think it runs inside another Claude
// Code session (which breaks its login), with claude's own updater off:
// Hoplet's copy is pinned.
func (h *Harness) Launch(spec harness.Spec) (harness.Launch, error) {
	if h.Binary == "" {
		return harness.Launch{}, errors.New("claude: no binary")
	}
	mode, err := nativeMode(spec.Mode)
	if err != nil {
		return harness.Launch{}, err
	}
	if spec.Plan {
		mode = planMode // spec.Mode is restored when plan mode ends
	}
	if spec.Effort != "" && !caps().HasEffort(spec.Effort) {
		return harness.Launch{}, fmt.Errorf("%w: effort %s", harness.ErrUnsupported, spec.Effort)
	}
	if spec.Fast {
		return harness.Launch{}, fmt.Errorf("%w: fast mode", harness.ErrUnsupported)
	}
	args := []string{
		h.Binary, "-p",
		"--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool", "stdio",
		// Allows switching to bypassPermissions later in the session; it
		// does not bypass anything by itself.
		"--allow-dangerously-skip-permissions",
		"--permission-mode", mode,
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--effort", string(spec.Effort))
	}
	if spec.Resume != "" {
		args = append(args, "--resume", spec.Resume)
	}
	if spec.Ephemeral {
		args = append(args, "--no-session-persistence")
	}
	return harness.Launch{Command: args, Env: launchEnv(spec.Env), Dir: spec.WorkDir}, nil
}

func launchEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") || name == "DISABLE_AUTOUPDATER" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DISABLE_AUTOUPDATER=1")
}

// Attach starts translating io's lines into events.
func (h *Harness) Attach(_ context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	return newRun(io, spec), nil
}

// Package codex drives Codex through `codex app-server`: JSON-RPC lines over
// stdio, one process and one thread per Hoplet session.
//
// Hoplet runs its own copy of the official Codex package (see Install),
// pinned to the version this adapter is verified against. Logins and
// configuration stay the user's: this package never reads CODEX_HOME.
package codex

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
const Kind = "codex"

// PinnedVersion is the Codex version Install fetches.
const PinnedVersion = "0.167.0"

// probeTimeout bounds `codex --version`.
const probeTimeout = 10 * time.Second

// recordedVersions are the versions this adapter's contract tests cover. Add
// a version only together with its recordings in testdata.
func recordedVersions() []string { return []string{PinnedVersion} }

func recorded(v string) bool { return slices.Contains(recordedVersions(), v) }

// BinaryPath is the managed codex binary in Hoplet's home.
func BinaryPath(home string) string {
	return filepath.Join(home, "harness", Kind, currentLink, "bin", "codex")
}

// ErrNotInstalled says Hoplet's copy of Codex is missing.
var ErrNotInstalled = errors.New("codex: Hoplet's copy of Codex is not installed (run `hoplet harness install codex`)")

// Harness is Codex.
type Harness struct {
	// Binary is the codex executable Hoplet runs (BinaryPath).
	Binary string

	mu     sync.Mutex
	cached harness.Info
	stamp  stamp // the binary the cached info is for
}

// stamp identifies a binary's contents cheaply.
type stamp struct {
	path string
	size int64
	mod  time.Time
}

var _ harness.StdioHarness = (*Harness)(nil)

// Kind reports "codex".
func (h *Harness) Kind() string { return Kind }

// caps is what this adapter supports. Without approvals, the only modes it
// can honestly offer are the one that asks (and is refused) and the one
// that never asks.
func caps() proto.Capabilities {
	return proto.Capabilities{
		Resume: true, PartialStream: true,
		Modes: []proto.PermissionMode{proto.ModeConfirm, proto.ModeFullAuto},
	}
}

// Probe runs `codex --version`, once per installed binary.
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
		h.cached = harness.Info{Version: v, Capabilities: caps(), Supported: recorded(v)}
		h.stamp = st
	}
	return h.cached, nil
}

func binaryStamp(bin string) (stamp, error) {
	path, err := filepath.EvalSymlinks(bin)
	if errors.Is(err, os.ErrNotExist) {
		return stamp{}, ErrNotInstalled
	}
	if err != nil {
		return stamp{}, fmt.Errorf("codex: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return stamp{}, fmt.Errorf("codex: %w", err)
	}
	return stamp{path: path, size: fi.Size(), mod: fi.ModTime()}, nil
}

// version runs bin --version and parses "codex-cli 0.167.0". The probe runs
// outside the harness environment filter, so it gets only PATH and HOME: no
// credential variable reaches it.
func version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("codex: %s --version: %w", bin, err)
	}
	return parseVersion(out)
}

func parseVersion(out []byte) (string, error) {
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != "codex-cli" {
		return "", fmt.Errorf("codex: unexpected --version output %q", strings.TrimSpace(string(out)))
	}
	return fields[1], nil
}

// native is how a Hoplet permission mode runs on Codex.
type native struct {
	approval, sandbox, reviewer string
}

func nativeMode(m proto.PermissionMode) (native, error) {
	switch m {
	case proto.ModeConfirm, "":
		return native{"untrusted", "workspace-write", "user"}, nil
	case proto.ModeAutoEdit:
		return native{"on-request", "workspace-write", "user"}, nil
	case proto.ModeAutoReview:
		return native{"on-request", "workspace-write", "auto_review"}, nil
	case proto.ModeFullAuto:
		return native{"never", "danger-full-access", "user"}, nil
	}
	return native{}, fmt.Errorf("codex: unknown permission mode %q", m)
}

// Launch returns the codex command line.
func (h *Harness) Launch(spec harness.Spec) (harness.Launch, error) {
	if h.Binary == "" {
		return harness.Launch{}, errors.New("codex: no binary")
	}
	if spec.Plan || spec.Fast || spec.Ultracode {
		return harness.Launch{}, fmt.Errorf("%w: plan, fast and ultracode", harness.ErrUnsupported)
	}
	if _, err := nativeMode(spec.Mode); err != nil {
		return harness.Launch{}, err
	}
	return harness.Launch{Command: []string{h.Binary, "app-server"}, Env: slices.Clone(spec.Env), Dir: spec.WorkDir}, nil
}

// Attach connects to a codex app-server: one just launched, or one already
// running under a shim when the daemon restarted.
func (h *Harness) Attach(ctx context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	r, err := newRun(io, spec)
	if err != nil {
		return nil, err
	}
	go r.loop()
	if err := r.start(ctx); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}

// Package hermes drives Hermes Agent through its gateway. A small bridge
// process (`hoplet hermes-bridge`) holds the gateway's WebSocket and runs
// behind a shim like any stdio harness: every gateway frame is a stdout
// line, every request a stdin line. The gateway owns the session; the
// bridge only carries it, so a daemon restart loses no events.
package hermes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/verified"
	"github.com/yangflow/hoplet/proto"
)

// Kind is this adapter's harness kind.
const Kind = "hermes"

// recordedVersions are the Hermes versions this adapter's recordings
// cover; other versions need to pass the self-check on this machine.
func recordedVersions() []string { return []string{"0.22.5"} }

func recorded(v string) bool { return slices.Contains(recordedVersions(), v) }

// StateDir is Hoplet's directory for this adapter in a Hoplet home.
func StateDir(home string) string { return filepath.Join(home, "harness", Kind) }

// probeTTL is how long a probe is reused: `hermes --version` takes a
// moment, and `hermes update` changes the version in place.
const probeTTL = time.Minute

// probeTimeout bounds `hermes --version`.
const probeTimeout = 20 * time.Second

// Harness is Hermes Agent, reached through the gateway of the Hermes home
// its environment names (by default ~/.hermes, shared with the user's own
// Hermes).
type Harness struct {
	// Exe is the hoplet executable, which runs the bridge.
	Exe string
	// Dir is Hoplet's directory for this adapter, where the versions that
	// passed the self-check here are kept.
	Dir string

	mu sync.Mutex
	// env returns the environment harnesses start with (see UseEnv);
	// Hermes is looked up on its PATH, else in ~/.local/bin.
	envFunc func() []string
	cached  harness.Info
	path    string
	probed  time.Time
	nowFunc func() time.Time
	// models are model.options' answer as of listed.
	models []proto.ModelInfo
	listed time.Time
}

var (
	_ harness.StdioHarness = (*Harness)(nil)
	_ harness.EnvUser      = (*Harness)(nil)
	_ harness.ModelLister  = (*Harness)(nil)
)

// Kind reports "hermes".
func (h *Harness) Kind() string { return Kind }

// ErrNotInstalled says no hermes executable was found.
var ErrNotInstalled = errors.New("hermes: Hermes Agent is not installed (no hermes on PATH or in ~/.local/bin)")

// caps is what this adapter supports. Hermes asks before edits per
// session, and its yolo skips command approvals; whether a command asks
// otherwise is Hermes's own judgment, so there is no separate auto-review
// level. Plan mode is only a prompt in Hermes, and its sessions created
// here have no question tool.
func caps() proto.Capabilities {
	return proto.Capabilities{
		Steer: true, Resume: true, SetMode: true, SetModel: true, PartialStream: true, Fast: true, MCPInject: true,
		Modes:   []proto.PermissionMode{proto.ModeConfirm, proto.ModeAutoEdit, proto.ModeFullAuto},
		Efforts: []proto.Effort{proto.EffortLow, proto.EffortMedium, proto.EffortHigh, proto.EffortXHigh, proto.EffortMax, proto.EffortUltra},
	}
}

// UseEnv sets where the harness environment comes from.
func (h *Harness) UseEnv(env func() []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.envFunc = env
}

func (h *Harness) env() []string {
	h.mu.Lock()
	f := h.envFunc
	h.mu.Unlock()
	if f == nil {
		return nil
	}
	return f()
}

func (h *Harness) now() time.Time {
	if h.nowFunc != nil {
		return h.nowFunc()
	}
	return time.Now()
}

// Find returns the hermes executable: on the environment's PATH, else in
// ~/.local/bin, where Hermes installs itself.
func Find(env []string) (string, error) {
	path, home := "", ""
	for _, kv := range env {
		switch {
		case len(kv) > 5 && kv[:5] == "PATH=":
			path = kv[5:]
		case len(kv) > 5 && kv[:5] == "HOME=":
			home = kv[5:]
		}
	}
	dirs := filepath.SplitList(path)
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, "hermes")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", ErrNotInstalled
}

// Probe runs `hermes --version`, reusing the answer for a minute.
func (h *Harness) Probe(ctx context.Context) (harness.Info, error) {
	env := h.env()
	path, err := Find(env)
	if err != nil {
		return harness.Info{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if path != h.path || h.now().Sub(h.probed) >= probeTTL {
		v, err := version(ctx, path, env)
		if err != nil {
			return harness.Info{}, err
		}
		h.cached = harness.Info{Version: v, Capabilities: caps()}
		h.path, h.probed = path, h.now()
	}
	return h.withState(h.cached), nil
}

// withState adds what Hoplet knows about the version on this machine:
// whether it passed the self-check, and the last check's outcome. Both are
// read each time, as a check may have run since the probe.
func (h *Harness) withState(info harness.Info) harness.Info {
	info.Supported = recorded(info.Version) || (h.Dir != "" && verified.Has(h.Dir, info.Version))
	if h.Dir != "" {
		if o, ok := verified.Last(h.Dir); ok {
			info.Notice, info.Attention = notice(o, info.Version)
		}
	}
	if !info.Supported && info.Notice == "" {
		info.Notice = "run `hoplet harness verify hermes` to check it on this machine"
	}
	return info
}

var versionRe = regexp.MustCompile(`Hermes Agent v(\d+\.\d+\.\d+)`)

// version runs hermes --version with only PATH and HOME from env, and
// parses "Hermes Agent v0.22.5 (...)".
func version(ctx context.Context, bin string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	for _, kv := range env {
		if len(kv) > 5 && (kv[:5] == "PATH=" || kv[:5] == "HOME=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("hermes: %s --version: %w", bin, err)
	}
	m := versionRe.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("hermes: unexpected --version output %q", firstLine(out))
	}
	return string(m[1]), nil
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}

// Launch runs the bridge, which finds the gateway through the Hermes on the
// session's PATH.
func (h *Harness) Launch(spec harness.Spec) (harness.Launch, error) {
	if h.Exe == "" {
		return harness.Launch{}, errors.New("hermes: no hoplet executable to run the bridge")
	}
	bin, err := Find(spec.Env)
	if err != nil {
		return harness.Launch{}, err
	}
	if _, err := nativeMode(spec.Mode); err != nil {
		return harness.Launch{}, err
	}
	return harness.Launch{Command: []string{h.Exe, "hermes-bridge", "--hermes", bin}, Env: spec.Env, Dir: spec.WorkDir}, nil
}

// nativeMode checks that Hermes has m.
func nativeMode(m proto.PermissionMode) (string, error) {
	if !slices.Contains(caps().Modes, m) {
		return "", fmt.Errorf("hermes: no permission mode %q", m)
	}
	policy, _ := editPolicy(m)
	return policy, nil
}

// PersistsMCP reports that Hermes keeps a session's MCP servers in the
// session: its token is issued once, not at every launch.
func (h *Harness) PersistsMCP() bool { return true }

// Attach starts translating the bridge's lines into events.
func (h *Harness) Attach(_ context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	if spec.Plan {
		return nil, harness.ErrUnsupported
	}
	return newRun(io, spec), nil
}

// notice is what doctor says about the last check while Hoplet sees
// version current.
func notice(o verified.Outcome, current string) (string, bool) {
	if o.Version != current {
		return "", false
	}
	day := o.At.Format(time.DateOnly)
	switch {
	case o.Error == "":
		return "verified on this machine on " + day, false
	case o.Attempts == 0:
		return fmt.Sprintf("could not check Hermes %s on %s: %s; Hoplet will try again", o.Version, day, o.Error), true
	case o.Attempts >= verified.MaxAttempts:
		return fmt.Sprintf("Hermes %s failed the self-check on %s (attempt %d of %d): %s; run `hoplet harness verify hermes` to try again",
			o.Version, day, o.Attempts, verified.MaxAttempts, o.Error), true
	}
	return fmt.Sprintf("Hermes %s failed the self-check on %s (attempt %d of %d): %s; Hoplet will try again",
		o.Version, day, o.Attempts, verified.MaxAttempts, o.Error), true
}

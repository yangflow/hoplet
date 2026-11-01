package daemon

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
)

// envHarness is a harness that wants the daemon's environment and records
// whether it had it whenever the daemon first uses it.
type envHarness struct {
	*fake.Harness

	mu     sync.Mutex
	vars   func() []string
	early  []string // methods called before UseEnv
	called []string // every call, in order
}

func (h *envHarness) UseEnv(vars func() []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.called = append(h.called, "UseEnv")
	h.vars = vars
}

func (h *envHarness) note(method string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.called = append(h.called, method)
	if h.vars == nil {
		h.early = append(h.early, method)
	}
}

func (h *envHarness) Probe(ctx context.Context) (harness.Info, error) {
	h.note("Probe")
	return h.Harness.Probe(ctx)
}

func (h *envHarness) Launch(spec harness.Spec) (harness.Launch, error) {
	h.note("Launch")
	return h.Harness.Launch(spec)
}

func (h *envHarness) Attach(ctx context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	h.note("Attach")
	return h.Harness.Attach(ctx, spec, io)
}

// Recovery reaches a harness only through a live shim, which a unit test
// does not have, so the order is checked on every method the daemon may
// call on a harness: none runs before UseEnv.
func TestEnvUsersGetTheDaemonEnvironmentFirst(t *testing.T) {
	home, script := newHome(t, fake.Script{})
	cfg := testConfig(home, script)
	user := &envHarness{Harness: &fake.Harness{Binary: fakeExe, Script: script}}
	cfg.Harnesses = map[string]harness.Harness{"fake": user}
	cfg.Env = env.Static{"PATH=/usr/bin", "HOPLET_ENV_PROBE=1"}
	start(t, cfg)
	user.mu.Lock()
	defer user.mu.Unlock()
	if user.vars == nil {
		t.Fatal("the harness was not given the environment")
	}
	if got := user.vars(); !slices.Contains(got, "HOPLET_ENV_PROBE=1") {
		t.Fatalf("the harness environment is %v", got)
	}
	if len(user.early) != 0 || user.called[0] != "UseEnv" {
		t.Fatalf("calls before UseEnv: %v (all: %v)", user.early, user.called)
	}
}

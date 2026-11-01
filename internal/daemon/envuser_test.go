package daemon

import (
	"slices"
	"testing"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
)

// envHarness is a harness that wants the daemon's environment.
type envHarness struct {
	*fake.Harness
	vars func() []string
}

func (h *envHarness) UseEnv(vars func() []string) { h.vars = vars }

func TestEnvUsersGetTheDaemonEnvironment(t *testing.T) {
	home, script := newHome(t, fake.Script{})
	cfg := testConfig(home, script)
	user := &envHarness{Harness: &fake.Harness{Binary: fakeExe, Script: script}}
	cfg.Harnesses = map[string]harness.Harness{"fake": user}
	cfg.Env = env.Static{"PATH=/usr/bin", "HOPLET_ENV_PROBE=1"}
	start(t, cfg)
	if user.vars == nil {
		t.Fatal("the harness was not given the environment")
	}
	if got := user.vars(); !slices.Contains(got, "HOPLET_ENV_PROBE=1") {
		t.Fatalf("the harness environment is %v", got)
	}
}

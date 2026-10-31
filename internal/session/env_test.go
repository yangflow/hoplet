package session

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
)

// envSpy is the fake harness, recording the environment of each launch.
type envSpy struct {
	*fake.Harness
	mu   sync.Mutex
	envs [][]string
}

func (s *envSpy) Launch(spec harness.Spec) (harness.Launch, error) {
	s.mu.Lock()
	s.envs = append(s.envs, slices.Clone(spec.Env))
	s.mu.Unlock()
	return s.Harness.Launch(spec)
}

func (s *envSpy) launched() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.envs)
}

func TestSetEnvAppliesToLaterStarts(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}})
	spy := &envSpy{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	m := e.managerWith(map[string]harness.Harness{"fake": spy})
	defer m.Close()
	ctx := context.Background()

	first := e.create(m)
	m.SetEnv([]string{"PATH=/bin", "FROM=refresh"})
	if _, err := m.Send(ctx, first.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(first.ID, "turn end", turnEnded)
	got := spy.launched()
	if len(got) != 1 || !slices.Equal(got[0], []string{"PATH=/bin", "FROM=refresh"}) {
		t.Fatalf("launch environments %v", got)
	}

	// A caller changing its slice afterwards changes nothing.
	vars := []string{"PATH=/bin", "N=2"}
	m.SetEnv(vars)
	vars[1] = "N=changed"
	second := e.create(m)
	if _, err := m.Send(ctx, second.ID, "go", "k2"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(second.ID, "turn end", turnEnded)
	if got := spy.launched(); len(got) != 2 || !slices.Equal(got[1], []string{"PATH=/bin", "N=2"}) {
		t.Fatalf("second launch environment %v", got)
	}
}

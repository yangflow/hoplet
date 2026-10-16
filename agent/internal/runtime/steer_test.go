package runtime_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func TestSteerIsAbsorbedByTheRunningTurn(t *testing.T) {
	e := newEnv(t, nil)
	file := e.session("s1")
	if err := e.m.Prompt("s1", "work 6"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	if err := e.m.Steer("s1", runtime.Prompt{Text: "change course"}); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready after the turn", func() bool {
		s := e.rec.states("s1")
		return len(s) >= 3 && s[len(s)-1] == runtime.StateReady
	})
	// One turn: the steer neither started a turn of its own nor changed the state.
	e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateReady)
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "steered: change course") {
		t.Fatalf("the turn did not absorb the steer:\n%s", b)
	}
	// The tool calls of the turn are each reported once, though the steer was written meanwhile.
	calls := 0
	for _, o := range e.rec.outputs("s1") {
		if o.Kind == runtime.OutputToolCall {
			calls++
		}
	}
	if calls != 6 {
		t.Fatalf("%d tool calls reported, want 6", calls)
	}
}

func TestSteerAfterTheLastGapRunsAsTheNextTurn(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "work 1"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	time.Sleep(200 * time.Millisecond) // past the only gap
	if err := e.m.Steer("s1", runtime.Prompt{Text: "say late"}); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "two turns", func() bool {
		s := e.rec.states("s1")
		return len(s) >= 5 && s[len(s)-1] == runtime.StateReady
	})
	e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateReady, runtime.StateRunning, runtime.StateReady)
}

func TestSteerThenInterruptRunsTheSteerAsASecondTurn(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "work 30"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateRunning)
	if err := e.m.Steer("s1", runtime.Prompt{Text: "say after"}); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	// Claude Code runs the steer it did not absorb as a second turn, so there are two results; neither turn
	// fails the session and the interrupted flag does not leak into the second.
	e.rec.waitFor(t, "the second turn", func() bool {
		s := e.rec.states("s1")
		return len(s) >= 5 && s[len(s)-1] == runtime.StateReady
	})
	e.expectStates("s1", runtime.StateStarting, runtime.StateRunning, runtime.StateReady, runtime.StateRunning, runtime.StateReady)
	if n := len(e.starts()); n != 1 {
		t.Fatalf("%d starts, want 1", n)
	}
}

func TestSteerIsRefusedWhenItCannotWork(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Steer("s1", runtime.Prompt{Text: "x"}); !errors.Is(err, runtime.ErrNotRunning) {
		t.Fatalf("steer without a process: %v", err)
	}
	if err := e.m.Prompt("s1", "tool echo hi"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateWaitingPermission)
	if err := e.m.Steer("s1", runtime.Prompt{Text: "x"}); !errors.Is(err, runtime.ErrPermissionPending) {
		t.Fatalf("steer during a permission request: %v", err)
	}
	if err := e.m.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "ready", func() bool {
		s := e.rec.states("s1")
		return s[len(s)-1] == runtime.StateReady
	})
	if err := e.m.Steer("s1", runtime.Prompt{Text: "x"}); !errors.Is(err, runtime.ErrNotRunning) {
		t.Fatalf("steer between turns: %v", err)
	}
}

func TestSteerNeedsTheCapability(t *testing.T) {
	rec := newRecorder()
	m := runtime.NewManager(runtime.Config{
		Driver: nowDriver{},
		Locate: func(string) (runtime.Location, error) { return runtime.Location{Dir: t.TempDir()}, nil },
		Emit:   rec.emit, MaxIdle: 5,
	})
	if err := m.Steer("s1", runtime.Prompt{Text: "x"}); !errors.Is(err, runtime.ErrSteerUnsupported) {
		t.Fatalf("steer of a driver without the capability: %v", err)
	}
}

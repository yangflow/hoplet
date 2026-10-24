package runtime_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func (r *recorder) closed(id string) []runtime.PermissionClosedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runtime.PermissionClosedEvent
	for _, ev := range r.events {
		if c, ok := ev.(runtime.PermissionClosedEvent); ok && c.SessionID == id {
			out = append(out, c)
		}
	}
	return out
}

// TestPermissionTimerPausesWhileTheCoreIsUnreachable checks ADR 0039: the timeout counts connected time only, and
// the request is closed with a report the core receives later.
func TestPermissionTimerPausesWhileTheCoreIsUnreachable(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) { c.PermissionTimeout = 300 * time.Millisecond })
	e.session("s1")
	if err := e.m.Prompt("s1", "tool echo hi"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "permission request", func() bool { return len(e.rec.permissions("s1")) == 1 })
	e.m.SetConnected(false)
	time.Sleep(600 * time.Millisecond)
	if got := e.m.Pending(); len(got) != 1 {
		t.Fatalf("the request was denied while the core was unreachable: %+v", got)
	}
	e.m.SetConnected(true)
	e.rec.waitFor(t, "closed", func() bool { return len(e.rec.closed("s1")) == 1 })
	if c := e.rec.closed("s1")[0]; c.Reason != "timeout" || c.RequestID != e.rec.permissions("s1")[0].RequestID {
		t.Fatalf("closed: %+v", c)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
}

func TestPermissionCapDeniesARequestTheCoreNeverSaw(t *testing.T) {
	e := newEnv(t, func(c *runtime.Config) {
		c.PermissionTimeout = time.Hour
		c.PermissionCap = 300 * time.Millisecond
	})
	e.m.SetConnected(false)
	e.session("s1")
	if err := e.m.Prompt("s1", "tool echo hi"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitFor(t, "closed", func() bool { return len(e.rec.closed("s1")) == 1 })
	e.rec.waitState(t, "s1", runtime.StateReady)
	var denied bool
	for _, o := range e.rec.outputs("s1") {
		if o.IsError && strings.Contains(o.Text, "while Hoplet was unreachable") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("the tool call was not denied: %+v", e.rec.outputs("s1"))
	}
}

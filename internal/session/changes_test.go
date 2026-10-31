package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
)

// waitChanges waits on the manager's Changes, and nothing else, until cond
// holds: a change the manager fails to signal times the test out.
func waitChanges(t *testing.T, m *Manager, what string, cond func() bool) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		ch := m.Changes()
		if cond() {
			return
		}
		select {
		case <-ch:
		case <-timer.C:
			t.Fatalf("no manager change signaled %s", what)
		}
	}
}

func TestChangesSignalTurnStartAndEnd(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	l, err := m.attached(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitChanges(t, m, "the message's turn as started", func() bool { return !l.pending.Load() })
	if _, open, err := e.store.TurnState(ctx, sess.ID); err != nil {
		t.Fatal(err)
	} else if open {
		// The turn may still be running; its end must be signaled too.
		waitChanges(t, m, "the turn's end", func() bool {
			_, open, err := e.store.TurnState(ctx, sess.ID)
			return err == nil && !open
		})
	}
	// What a queue drainer does: try, and on ErrBusy wait for a change.
	waitChanges(t, m, "a change that lets the next send through", func() bool {
		_, err := m.Send(ctx, sess.ID, "two", "k2")
		if err != nil && !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		return err == nil
	})
}

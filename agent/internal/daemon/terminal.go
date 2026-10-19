package daemon

import (
	"context"
	"errors"
	"log"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/terminal"
)

type sessionDeleter = func(context.Context, string, bool, bool, bool) []sync.DeletionStep

// terminals connects the terminal package to the core connection and to the rules of ADR 0026: a terminal opens
// only on a session whose process is idle and not being moved, and closes when one of those stops being true.
type terminals struct {
	cfg    *config.Config
	mgr    *terminal.Manager
	bridge *bridge
	moves  *handoffs
	locate runtime.Locator
}

// newTerminals builds the manager; client returns the core client (nil until it exists). Output goes on the
// control lane; a message sent while the core is away is dropped, and the disconnect closes the terminal anyway.
func newTerminals(cfg *config.Config, deviceID string, bridge *bridge, moves *handoffs, locate runtime.Locator, client func() *sync.Client) *terminals {
	send := func(f func(c *sync.Client) error) {
		if c := client(); c != nil {
			if err := f(c); err != nil && !errors.Is(err, sync.ErrNotConnected) {
				log.Printf("[terminal] send: %v", err)
			}
		}
	}
	return &terminals{
		cfg: cfg, bridge: bridge, moves: moves, locate: locate,
		mgr: terminal.New(terminal.Config{
			Home: cfg.HopletHome, DeviceID: deviceID,
			Events: terminal.Events{
				Opened: func(id string) { send(func(c *sync.Client) error { return c.SendTerminalOpened(id) }) },
				Output: func(id string, seq int64, data []byte) {
					send(func(c *sync.Client) error { return c.SendTerminalOutput(id, seq, data) })
				},
				Closed: func(id, reason string, exitCode *int) {
					send(func(c *sync.Client) error { return c.SendTerminalClosed(id, reason, exitCode) })
				},
			},
			Log: log.Printf,
		}),
	}
}

// guard is the state check of terminal.open, run by the manager just before the shell starts.
func (t *terminals) guard(sessionID string) func() error {
	return func() error {
		switch t.bridge.managerOf(sessionID).State(sessionID) {
		case runtime.StateStarting, runtime.StateRunning, runtime.StateWaitingPermission:
			return &terminal.OpenError{Reason: terminal.ReasonTurnRunning}
		case runtime.StateExternal:
			return &terminal.OpenError{Reason: terminal.ReasonExternal}
		}
		if t.moves.moving(sessionID) {
			return &terminal.OpenError{Reason: terminal.ReasonHandoffRunning}
		}
		return nil
	}
}

func (t *terminals) open(m sync.TerminalOpen) error {
	loc, err := t.locate(m.SessionID)
	if err != nil || loc.Dir == "" {
		return &terminal.OpenError{Reason: terminal.ReasonNotADirectory}
	}
	err = t.mgr.Open(m.TerminalID, m.SessionID, loc.Dir, m.Cols, m.Rows, t.guard(m.SessionID))
	var oe *terminal.OpenError
	if err != nil && !errors.As(err, &oe) {
		log.Printf("[terminal] open %s: %v", shortIDPrefix(m.TerminalID), err)
		return &terminal.OpenError{Reason: terminal.ReasonClosed}
	}
	return err
}

func (t *terminals) hooks() sync.TerminalHooks {
	return sync.TerminalHooks{
		Enabled:      t.cfg.Terminal,
		Open:         t.open,
		Input:        t.mgr.Input,
		Resize:       t.mgr.Resize,
		Ack:          t.mgr.Ack,
		Close:        t.mgr.Close,
		Disconnected: func() { t.mgr.CloseAll(terminal.ReasonOwnerOffline) },
	}
}

// turnStarted runs on the runtime's goroutine under its lock, so the close happens elsewhere.
func (t *terminals) turnStarted(sessionID string) {
	go t.mgr.CloseSession(sessionID, terminal.ReasonTurnStarted)
}

// handoffStarted closes the session's terminal before the handoff stops the session and reads its files.
func (t *terminals) handoffStarted(sessionID string) {
	t.mgr.CloseSession(sessionID, terminal.ReasonHandoff)
}

// wrapDelete closes the session's terminal before the deletion touches its directory.
func (t *terminals) wrapDelete(inner sessionDeleter) sessionDeleter {
	return func(ctx context.Context, sessionID string, vendorFile, worktree, worktreeDirty bool) []sync.DeletionStep {
		t.mgr.CloseSession(sessionID, terminal.ReasonDeleted)
		return inner(ctx, sessionID, vendorFile, worktree, worktreeDirty)
	}
}

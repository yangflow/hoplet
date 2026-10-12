package sync

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
)

// HandoffVerification is what a target proves about its copy of a handed-off session.
type HandoffVerification struct {
	SessionBytes  int64
	SessionSHA256 string
	TreeHash      string
}

// HandoffWorktree is where a target installed a handed-off session: the worktree and its branch.
type HandoffWorktree struct {
	Worktree string
	Branch   string
}

// HandoffHooks are the device-side handlers of the handoff steps (docs/spec.md#handoff). The client calls
// them one at a time, in the order the core sent the messages, and answers for them: a nil error is the
// success message of the step, an error is `handoff.failed` with the error's text as the reason.
type HandoffHooks struct {
	// Prepare (owner) stops the session's process, takes a snapshot and uploads it; it returns the snapshot's
	// ID and the size of the session file's complete lines.
	Prepare func(ctx context.Context, sessionID, handoffID string) (snapshotID string, byteSize int64, err error)
	// Begin (target) opens the announced transfer.
	Begin func(ctx context.Context, m HandoffInstall) error
	// Data (target) takes one chunk. The chunk that completes the transfer also installs it and returns the
	// worktree, and the client then answers `handoff.installed`; earlier chunks return nil.
	Data func(ctx context.Context, m HandoffData) (installed *HandoffWorktree, err error)
	// Verify (target) reads back the installed copy.
	Verify func(ctx context.Context, sessionID, handoffID string) (HandoffVerification, error)
	// Commit is called after the client has moved the session's ownership; the new owner starts syncing at
	// the cursor, the old one stops controlling the session.
	Commit func(m HandoffCommit)
	// Abort (target) removes what the handoff installed.
	Abort func(m HandoffAbort)
	// Welcomed is called, in its own goroutine, after every connection with the sessions the core holds.
	Welcomed func(sessions []SessionMeta)
}

// enqueueHandoff runs handoff messages one at a time, in arrival order: a transfer's chunks must be applied
// in order, and a step waits for the one before it.
func (c *Client) enqueueHandoff(cs *connState, job func()) {
	c.handoffMu.Lock()
	previous := c.handoffTail
	done := make(chan struct{})
	c.handoffTail = done
	c.handoffMu.Unlock()
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		select {
		case <-cs.done:
			return
		default:
			job()
		}
	}()
}

func (c *Client) failHandoff(cs *connState, sessionID, handoffID, step string, err error) {
	log.Printf("[agent] handoff %s %s failed for %s: %v", shortID(handoffID), step, shortID(sessionID), err)
	c.mu.Lock()
	if len(c.failedHandoffs) > 64 {
		c.failedHandoffs = map[string]bool{}
	}
	c.failedHandoffs[handoffID] = true
	c.mu.Unlock()
	_ = c.writeMsg(cs, HandoffFailed{
		V: ProtocolVersion, Type: TypeHandoffFailed, SessionID: sessionID, HandoffID: handoffID,
		Step: step, Reason: err.Error(),
	})
}

func (c *Client) handoffFailed(handoffID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.failedHandoffs[handoffID]
}

// handleHandoff applies one handoff message of the core.
func (c *Client) handleHandoff(cs *connState, typ string, raw json.RawMessage) {
	ctx := context.Background()
	h := &c.hooks.Handoff
	missing := errors.New("not-implemented")
	switch typ {
	case TypeHandoffPrepare:
		var m HandoffPrepare
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		if h.Prepare == nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "prepare", missing)
			return
		}
		snapshotID, size, err := h.Prepare(ctx, m.SessionID, m.HandoffID)
		if err != nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "prepare", err)
			return
		}
		_ = c.writeMsg(cs, HandoffPrepared{
			V: ProtocolVersion, Type: TypeHandoffPrepared, SessionID: m.SessionID, HandoffID: m.HandoffID,
			SnapshotID: snapshotID, ByteSize: size,
		})
	case TypeHandoffInstall:
		var m HandoffInstall
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		c.mu.Lock()
		delete(c.failedHandoffs, m.HandoffID)
		c.mu.Unlock()
		if h.Begin == nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "install", missing)
			return
		}
		if err := h.Begin(ctx, m); err != nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "install", err)
		}
	case TypeHandoffData:
		var m HandoffData
		if json.Unmarshal(raw, &m) != nil || c.handoffFailed(m.HandoffID) {
			return
		}
		if h.Data == nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "install", missing)
			return
		}
		installed, err := h.Data(ctx, m)
		if err != nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "install", err)
			return
		}
		if installed != nil {
			_ = c.writeMsg(cs, HandoffInstalled{
				V: ProtocolVersion, Type: TypeHandoffInstalled, SessionID: m.SessionID, HandoffID: m.HandoffID,
				Worktree: installed.Worktree, Branch: installed.Branch,
			})
		}
	case TypeHandoffVerify:
		var m HandoffVerify
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		if h.Verify == nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "verify", missing)
			return
		}
		v, err := h.Verify(ctx, m.SessionID, m.HandoffID)
		if err != nil {
			c.failHandoff(cs, m.SessionID, m.HandoffID, "verify", err)
			return
		}
		_ = c.writeMsg(cs, HandoffVerified{
			V: ProtocolVersion, Type: TypeHandoffVerified, SessionID: m.SessionID, HandoffID: m.HandoffID,
			SessionBytes: v.SessionBytes, SessionSHA256: v.SessionSHA256, TreeHash: v.TreeHash,
		})
	case TypeHandoffCommit:
		var m HandoffCommit
		if json.Unmarshal(raw, &m) != nil || m.ByteSize < 0 || m.OwnerDeviceID == "" {
			return
		}
		c.mu.Lock()
		delete(c.failedHandoffs, m.HandoffID)
		c.mu.Unlock()
		if m.OwnerDeviceID == c.deviceID {
			c.AcceptOwnership(m.SessionID, m.ByteSize, m.OwnerEpoch)
		} else {
			c.Disown(m.SessionID, m.OwnerEpoch)
		}
		if h.Commit != nil {
			h.Commit(m)
		}
		c.notifyWake()
	case TypeHandoffAbort:
		var m HandoffAbort
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		c.mu.Lock()
		delete(c.failedHandoffs, m.HandoffID)
		c.mu.Unlock()
		if h.Abort != nil {
			h.Abort(m)
		}
	}
}

// isHandoffMessage tells the messages handleHandoff takes.
func isHandoffMessage(typ string) bool {
	return strings.HasPrefix(typ, "handoff.")
}

// AcceptOwnership makes this device the writer of the session from `cursor` on: the core holds that many
// bytes and the next append starts there.
func (c *Client) AcceptOwnership(sessionID string, cursor int64, epoch int64) {
	c.mu.Lock()
	c.acceptOwnershipLocked(sessionID, cursor)
	c.ownerEpochs[sessionID] = epoch
	c.mu.Unlock()
	c.notifyWake()
}

// Disown stops this device from syncing the session: another device owns it from `epoch` on.
func (c *Client) Disown(sessionID string, epoch int64) {
	c.mu.Lock()
	c.suppressed[sessionID] = true
	c.ownerEpochs[sessionID] = epoch
	c.mu.Unlock()
}

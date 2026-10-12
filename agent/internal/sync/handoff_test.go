package sync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func handoffClient(t *testing.T, hooks HandoffHooks) (*Client, *connState, func() map[string]any) {
	t.Helper()
	c, cs, read := dialTestConn(t)
	c.deviceID = "me"
	c.hooks.Handoff = hooks
	return c, cs, read
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPrepareIsAnsweredWithTheSnapshotAndTheFinalSize(t *testing.T) {
	c, cs, read := handoffClient(t, HandoffHooks{
		Prepare: func(_ context.Context, session, handoff string) (string, int64, error) {
			if session != "s1" || handoff != "h1" {
				t.Fatalf("prepare(%q, %q)", session, handoff)
			}
			return "snap1", 4242, nil
		},
	})
	c.handleHandoff(cs, TypeHandoffPrepare, raw(t, HandoffPrepare{SessionID: "s1", HandoffID: "h1"}))
	got := read()
	if got["type"] != TypeHandoffPrepared || got["snapshotId"] != "snap1" || got["byteSize"] != float64(4242) ||
		got["handoffId"] != "h1" || got["sessionId"] != "s1" {
		t.Fatalf("got %#v", got)
	}
}

func TestAFailedStepIsReportedOnceWithItsReason(t *testing.T) {
	calls := 0
	c, cs, read := handoffClient(t, HandoffHooks{
		Prepare: func(context.Context, string, string) (string, int64, error) {
			return "", 0, errors.New("not-a-git-repository")
		},
		Begin: func(context.Context, HandoffInstall) error { return nil },
		Data: func(context.Context, HandoffData) (*HandoffWorktree, error) {
			calls++
			return nil, errors.New("invalid-transfer: checksum")
		},
	})
	c.handleHandoff(cs, TypeHandoffPrepare, raw(t, HandoffPrepare{SessionID: "s1", HandoffID: "h1"}))
	got := read()
	if got["type"] != TypeHandoffFailed || got["step"] != "prepare" || got["reason"] != "not-a-git-repository" {
		t.Fatalf("got %#v", got)
	}

	c.handleHandoff(cs, TypeHandoffInstall, raw(t, HandoffInstall{SessionID: "s1", HandoffID: "h2"}))
	for i := 0; i < 3; i++ {
		c.handleHandoff(cs, TypeHandoffData, raw(t, HandoffData{SessionID: "s1", HandoffID: "h2", Part: "snapshot", Seq: i}))
	}
	got = read()
	if got["type"] != TypeHandoffFailed || got["step"] != "install" || got["handoffId"] != "h2" {
		t.Fatalf("got %#v", got)
	}
	if calls != 1 {
		t.Fatalf("chunks after the failure reached the hook: %d calls", calls)
	}
	// The next message is the next thing sent: no second failure was written.
	c.handleHandoff(cs, TypeHandoffAbort, raw(t, HandoffAbort{SessionID: "s1", HandoffID: "h2"}))
	if err := c.writeMsg(cs, PingMsg{V: ProtocolVersion, Type: TypePingMsg}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got["type"] != TypePingMsg {
		t.Fatalf("got %#v", got)
	}
}

func TestTheChunkThatInstallsIsAnsweredInstalled(t *testing.T) {
	var seen []string
	c, cs, read := handoffClient(t, HandoffHooks{
		Begin: func(_ context.Context, m HandoffInstall) error {
			seen = append(seen, "begin:"+m.TargetPath)
			return nil
		},
		Data: func(_ context.Context, m HandoffData) (*HandoffWorktree, error) {
			seen = append(seen, "data")
			if !m.Last {
				return nil, nil
			}
			return &HandoffWorktree{Worktree: "/p-s1", Branch: "hoplet/s1"}, nil
		},
		Verify: func(_ context.Context, session, handoff string) (HandoffVerification, error) {
			return HandoffVerification{SessionBytes: 7, SessionSHA256: "abc", TreeHash: "def"}, nil
		},
	})
	c.handleHandoff(cs, TypeHandoffInstall, raw(t, HandoffInstall{SessionID: "s1", HandoffID: "h1", TargetPath: "/p"}))
	c.handleHandoff(cs, TypeHandoffData, raw(t, HandoffData{SessionID: "s1", HandoffID: "h1", Seq: 0}))
	c.handleHandoff(cs, TypeHandoffData, raw(t, HandoffData{SessionID: "s1", HandoffID: "h1", Seq: 1, Last: true}))
	if got := read(); got["type"] != TypeHandoffInstalled || got["handoffId"] != "h1" || got["worktree"] != "/p-s1" || got["branch"] != "hoplet/s1" {
		t.Fatalf("got %#v", got)
	}
	c.handleHandoff(cs, TypeHandoffVerify, raw(t, HandoffVerify{SessionID: "s1", HandoffID: "h1"}))
	got := read()
	if got["type"] != TypeHandoffVerified || got["sessionBytes"] != float64(7) || got["sessionSha256"] != "abc" || got["treeHash"] != "def" {
		t.Fatalf("got %#v", got)
	}
	if len(seen) != 3 || seen[0] != "begin:/p" {
		t.Fatalf("seen = %v", seen)
	}
}

func TestCommitMovesOwnershipOnTheClientBeforeTheHookRuns(t *testing.T) {
	var cursorAtHook int64
	var committed HandoffCommit
	var c *Client
	c, cs, _ := handoffClient(t, HandoffHooks{
		Commit: func(m HandoffCommit) {
			committed = m
			c.mu.RLock()
			cursorAtHook = c.cursors[m.SessionID]
			c.mu.RUnlock()
		},
	})
	c.suppressed["s1"] = true
	c.handleHandoff(cs, TypeHandoffCommit, raw(t, HandoffCommit{SessionID: "s1", HandoffID: "h1", OwnerDeviceID: "me", OwnerEpoch: 3, ByteSize: 900}))
	if cursorAtHook != 900 || committed.OwnerEpoch != 3 {
		t.Fatalf("cursor at the hook %d, commit %+v", cursorAtHook, committed)
	}
	if c.suppressed["s1"] || c.ownerEpochs["s1"] != 3 {
		t.Fatalf("suppressed %v, epoch %d", c.suppressed["s1"], c.ownerEpochs["s1"])
	}

	// A commit that names another owner takes the session away from this client.
	c.handleHandoff(cs, TypeHandoffCommit, raw(t, HandoffCommit{SessionID: "s2", HandoffID: "h2", OwnerDeviceID: "other", OwnerEpoch: 1, ByteSize: 10}))
	if !c.suppressed["s2"] || c.ownerEpochs["s2"] != 1 {
		t.Fatalf("suppressed %v, epoch %d", c.suppressed["s2"], c.ownerEpochs["s2"])
	}
	// Nonsense is ignored.
	c.handleHandoff(cs, TypeHandoffCommit, raw(t, HandoffCommit{SessionID: "s3", HandoffID: "h3", OwnerDeviceID: "me", ByteSize: -1}))
	if _, ok := c.cursors["s3"]; ok {
		t.Fatal("a negative cursor was accepted")
	}
}

func TestHandoffMessagesWithoutHooksAreRefused(t *testing.T) {
	c, cs, read := handoffClient(t, HandoffHooks{})
	c.handleHandoff(cs, TypeHandoffPrepare, raw(t, HandoffPrepare{SessionID: "s1", HandoffID: "h1"}))
	if got := read(); got["type"] != TypeHandoffFailed || got["reason"] != "not-implemented" {
		t.Fatalf("got %#v", got)
	}
}

func TestHandoffMessagesRunInArrivalOrder(t *testing.T) {
	var order []int
	done := make(chan struct{})
	c, cs, _ := handoffClient(t, HandoffHooks{})
	for i := 0; i < 5; i++ {
		c.enqueueHandoff(cs, func() { order = append(order, i) })
	}
	c.enqueueHandoff(cs, func() { close(done) })
	<-done
	for i, v := range order {
		if v != i {
			t.Fatalf("order = %v", order)
		}
	}
}

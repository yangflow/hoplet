package sync

import (
	"context"
	"testing"
)

func TestStartInANewWorktreeIsAnsweredWithIt(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.Runtime.Start = func(_ context.Context, req RuntimeStart) (RuntimeStartResult, error) {
		if req.Worktree == nil || req.Worktree.Branch != "feat/x" {
			t.Fatalf("start = %+v", req)
		}
		return RuntimeStartResult{SessionID: "new-2", Worktree: &StartedWorktree{Clone: "/work/a", Path: "/work/a-feat-x", Branch: "feat/x"}}, nil
	}
	c.hooks.Runtime.Started = func(string) {}
	c.handleRuntimeStart(cs, []byte(`{"v":20,"type":"runtime.start","requestId":"r4","projectPath":"/work/a","provider":"claude",`+
		`"text":"go","promptId":"p4","worktree":{"branch":"feat/x"}}`))
	msg := read()
	wt, _ := msg["worktree"].(map[string]any)
	if msg["type"] != TypeRuntimeStarted || msg["sessionId"] != "new-2" || wt["clone"] != "/work/a" || wt["path"] != "/work/a-feat-x" || wt["branch"] != "feat/x" {
		t.Fatalf("got %#v", msg)
	}
}

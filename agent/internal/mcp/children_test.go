package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// childCore answers the children routes with the body of each call in turn (the last one repeats) and records them.
func childCore(t *testing.T, status int, bodies ...string) (*Core, *[]string) {
	t.Helper()
	f := newFakeCore(t)
	var seen []string
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(srv.Close)
	c := NewCore(srv.URL, f.tokens)
	c.waitMax, c.pollEvery = 300*time.Millisecond, 10*time.Millisecond
	return c, &seen
}

const (
	oneRunning = `{"sessionId":"s1","children":[{"sessionId":"c1","state":"running","elapsedSeconds":1},{"sessionId":"c2","state":"done","elapsedSeconds":2}]}`
	allDone    = `{"sessionId":"s1","children":[{"sessionId":"c1","state":"done","elapsedSeconds":3},{"sessionId":"c2","state":"done","elapsedSeconds":2}]}`
)

func TestWaitChildrenPollsUntilAllSettle(t *testing.T) {
	c, seen := childCore(t, 200, oneRunning, oneRunning, allDone)
	rep := c.Call(context.Background(), "s1", ToolWaitChildren, json.RawMessage(`{"sessionIds":["c1","c2"]}`))
	if rep.IsError || !strings.Contains(rep.Text, `"pending":false`) || !strings.Contains(rep.Text, `"state":"done"`) {
		t.Fatalf("rep = %+v", rep)
	}
	if len(*seen) != 3 || (*seen)[0] != "GET /api/sessions/s1/children/status?ids=c1%2Cc2" {
		t.Fatalf("seen = %v", *seen)
	}
}

func TestWaitChildrenAnyReturnsAtOnceAndTimeoutIsPending(t *testing.T) {
	c, seen := childCore(t, 200, oneRunning)
	rep := c.Call(context.Background(), "s1", ToolWaitChildren, json.RawMessage(`{"until":"any"}`))
	if rep.IsError || !strings.Contains(rep.Text, `"pending":false`) || len(*seen) != 1 || (*seen)[0] != "GET /api/sessions/s1/children/status" {
		t.Fatalf("rep = %+v seen = %v", rep, *seen)
	}
	rep = c.Call(context.Background(), "s1", ToolWaitChildren, json.RawMessage(`{"until":"all","timeoutSeconds":1}`))
	if rep.IsError || !strings.Contains(rep.Text, `"pending":true`) {
		t.Fatalf("a wait that runs out is pending: %+v", rep)
	}
}

func TestWaitChildrenWithoutChildrenReturnsAtOnce(t *testing.T) {
	c, seen := childCore(t, 200, `{"sessionId":"s1","children":[]}`)
	rep := c.Call(context.Background(), "s1", ToolWaitChildren, json.RawMessage(``))
	if rep.IsError || !strings.Contains(rep.Text, `"pending":false`) || len(*seen) != 1 {
		t.Fatalf("rep = %+v seen = %v", rep, *seen)
	}
}

func TestWaitChildrenStopsWithTheCallAndValidates(t *testing.T) {
	c, _ := childCore(t, 200, oneRunning)
	c.waitMax, c.pollEvery = time.Minute, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if rep := c.Call(ctx, "s1", ToolWaitChildren, json.RawMessage(`{}`)); !rep.IsError {
		t.Fatalf("a cancelled call is an error: %+v", rep)
	}
	many := `{"sessionIds":["` + strings.Repeat(`a","`, maxWaitIDs) + `a"]}`
	for _, bad := range []string{`{"until":"some"}`, `{"timeoutSeconds":0}`, `{"timeoutSeconds":51}`, `{"sessionIds":["a b"]}`, many, `[`} {
		c, seen := childCore(t, 200, oneRunning)
		if rep := c.Call(context.Background(), "s1", ToolWaitChildren, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("%.40s was accepted", bad)
		}
		if len(*seen) != 0 {
			t.Fatalf("an invalid call reached the core: %v", *seen)
		}
	}
}

func TestCollectChildPostsAndPassesTheAnswerThrough(t *testing.T) {
	c, seen := childCore(t, 200, `{"sessionId":"c1","state":"done","summary":"did it","summaryTruncated":false,"released":true}`)
	rep := c.Call(context.Background(), "s1", ToolCollectChild, json.RawMessage(`{"sessionId":"c1"}`))
	if rep.IsError || !strings.Contains(rep.Text, `"summary":"did it"`) || !strings.HasPrefix(rep.Text, dataNotice) {
		t.Fatalf("rep = %+v", rep)
	}
	rep = c.Call(context.Background(), "s1", ToolCollectChild, json.RawMessage(`{"sessionId":"c1","release":false}`))
	if rep.IsError || (*seen)[0] != "POST /api/sessions/s1/children/c1/collect" || (*seen)[1] != "POST /api/sessions/s1/children/c1/collect?release=false" {
		t.Fatalf("seen = %v", *seen)
	}
	for _, bad := range []string{``, `{}`, `{"sessionId":"../x"}`} {
		if rep := c.Call(context.Background(), "s1", ToolCollectChild, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

func TestCollectChildRelaysRefusals(t *testing.T) {
	c, _ := childCore(t, 403, `{"code":"forbidden","reason":"not-a-child"}`)
	rep := c.Call(context.Background(), "s1", ToolCollectChild, json.RawMessage(`{"sessionId":"c9"}`))
	if !rep.IsError || !strings.Contains(rep.Text, "forbidden") || !strings.Contains(rep.Text, "not-a-child") {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestReadChildDiffAsksOnePage(t *testing.T) {
	c, seen := childCore(t, 200, `{"diff":"@@ -1 +1 @@","next":4096,"truncated":true}`)
	rep := c.Call(context.Background(), "s1", ToolReadChildDiff, json.RawMessage(`{"sessionId":"c1","path":"src/a b.ts","cursor":4096}`))
	if rep.IsError || !strings.Contains(rep.Text, `"next":4096`) {
		t.Fatalf("rep = %+v", rep)
	}
	want := "GET /api/sessions/s1/children/c1/diff?cursor=4096&limit=12288&path=src%2Fa+b.ts"
	if (*seen)[0] != want {
		t.Fatalf("request = %s, want %s", (*seen)[0], want)
	}
	for _, bad := range []string{`{"sessionId":"c1"}`, `{"sessionId":"c1","path":"  "}`, `{"sessionId":"c1","path":"a","cursor":-1}`, `{"path":"a"}`} {
		if rep := c.Call(context.Background(), "s1", ToolReadChildDiff, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

func TestCancelChildNamesWhatWasCancelled(t *testing.T) {
	c, seen := childCore(t, 200, `{"ok":true,"cancelled":["c1","g1"]}`)
	rep := c.Call(context.Background(), "s1", ToolCancelChild, json.RawMessage(`{"sessionId":"c1","subtree":true}`))
	if rep.IsError || !strings.Contains(rep.Text, "c1, g1") || (*seen)[0] != "POST /api/sessions/s1/children/c1/cancel?subtree=true" {
		t.Fatalf("rep = %+v seen = %v", rep, *seen)
	}
	rep = c.Call(context.Background(), "s1", ToolCancelChild, json.RawMessage(`{"sessionId":"c1"}`))
	if rep.IsError || (*seen)[1] != "POST /api/sessions/s1/children/c1/cancel" {
		t.Fatalf("seen = %v", *seen)
	}
	for _, bad := range []string{``, `{}`, `{"sessionId":"s1"}`, `{"sessionId":"a b"}`} {
		if rep := c.Call(context.Background(), "s1", ToolCancelChild, json.RawMessage(bad)); !rep.IsError {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

func TestStartSessionWorktreeTitleAndNested(t *testing.T) {
	c, seen := startCore(t, 200, `{"ok":true,"sessionId":"n1","promptId":"x"}`, "")
	rep := c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(`{"prompt":"go","worktree":false,"title":" Fix tests ","nested":true}`))
	if rep.IsError || !strings.Contains(rep.Text, "does not wait") {
		t.Fatalf("rep = %+v", rep)
	}
	got := lastPost(*seen)
	for _, w := range []string{`"worktree":false`, `"title":"Fix tests"`, `"nested":true`} {
		if !strings.Contains(got, w) {
			t.Fatalf("request %s lacks %s", got, w)
		}
	}
	c, seen = startCore(t, 200, `{"ok":true,"sessionId":"n1","promptId":"x"}`, "")
	c.Call(context.Background(), "s1", ToolStartSession, json.RawMessage(`{"prompt":"go","branch":"feat/y"}`))
	if got := lastPost(*seen); !strings.Contains(got, `"worktree":{"branch":"feat/y"}`) || strings.Contains(got, `"nested"`) {
		t.Fatalf("a branch alone asks for a worktree: %s", got)
	}
}

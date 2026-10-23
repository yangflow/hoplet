package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func TestHandleSessionsRemoveSuccess(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.SessionsRemove = func(ctx context.Context, path string, ids []string) ([]string, []DeviceProject, error) {
		if path != "/tmp/app" || len(ids) != 1 || ids[0] != "s1" {
			t.Fatalf("path=%q ids=%v", path, ids)
		}
		return []string{}, []DeviceProject{{Path: path, Sessions: []DeviceProjectSession{}}}, nil
	}
	raw, _ := json.Marshal(map[string]any{
		"type": "device.sessions.remove", "requestId": "r1", "path": "/tmp/app", "sessionIds": []string{"s1"},
	})
	c.handleSessions(cs, raw)
	msg := read()
	if msg["type"] != "device.projects" || msg["requestId"] != "r1" {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleSessionsImportRejected(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.SessionsImport = func(ctx context.Context, path string, ids []string) ([]string, []DeviceProject, error) {
		return nil, nil, errors.New("empty-session-ids")
	}
	raw, _ := json.Marshal(map[string]any{
		"type": "device.sessions.import", "requestId": "r2", "path": "/tmp/app", "sessionIds": []string{},
	})
	c.handleSessions(cs, raw)
	msg := read()
	if msg["type"] != "device.projects.rejected" || msg["reason"] != "empty-session-ids" {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleProjectsList(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.ProjectsList = func(ctx context.Context) ([]string, []DeviceProject, error) {
		return []string{"/cand"}, []DeviceProject{{Path: "/listed"}}, nil
	}
	raw, _ := json.Marshal(map[string]any{"type": "device.projects.list", "requestId": "r3"})
	c.handleProjects(cs, raw)
	msg := read()
	if msg["type"] != "device.projects" {
		t.Fatalf("got %#v", msg)
	}
	cands, _ := msg["candidates"].([]any)
	if len(cands) != 1 || cands[0] != "/cand" {
		t.Fatalf("candidates %#v", msg["candidates"])
	}
}

func TestHandleProjectsRemove(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.ProjectsRemove = func(ctx context.Context, path string) ([]string, []DeviceProject, error) {
		if path != "/tmp/app" {
			t.Fatalf("path=%q", path)
		}
		return []string{}, []DeviceProject{}, nil
	}
	raw, _ := json.Marshal(map[string]any{
		"type": "device.projects.remove", "requestId": "r4", "path": "/tmp/app",
	})
	c.handleProjects(cs, raw)
	msg := read()
	if msg["type"] != "device.projects" || msg["requestId"] != "r4" {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleDirsListing(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.DirsList = func(ctx context.Context, path string) (DirListing, error) {
		if path != "~" {
			t.Fatalf("path=%q", path)
		}
		return DirListing{Path: "/home/me", Root: "/home/me", Entries: []DirEntry{{Name: "app", Git: true}}}, nil
	}
	c.handleDirs(cs, "r5", "~")
	msg := read()
	if msg["type"] != "device.dirs" || msg["requestId"] != "r5" || msg["parent"] != nil || msg["truncated"] != false {
		t.Fatalf("got %#v", msg)
	}
	entries, _ := msg["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries %#v", msg["entries"])
	}
}

func TestHandleDirsRejected(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.DirsList = func(ctx context.Context, path string) (DirListing, error) {
		return DirListing{}, errors.New("outside the browse root")
	}
	c.handleDirs(cs, "r6", "/etc")
	msg := read()
	if msg["type"] != "device.dirs.rejected" || msg["requestId"] != "r6" || msg["reason"] != "outside the browse root" {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleCloneNamesTheCloneOrNull(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.CloneFind = func(ctx context.Context, origin string) (string, error) {
		if origin == "git@example.test:o/r.git" {
			return "/home/me/r", nil
		}
		return "", nil
	}
	c.handleClone(cs, "r7", "git@example.test:o/r.git")
	if msg := read(); msg["type"] != "device.clone" || msg["requestId"] != "r7" || msg["path"] != "/home/me/r" {
		t.Fatalf("got %#v", msg)
	}
	c.handleClone(cs, "r8", "git@example.test:o/other.git")
	msg := read()
	if path, present := msg["path"]; msg["type"] != "device.clone" || !present || path != nil {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleCloneCheckAnswersTheHookOrNoOrigin(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.handleCloneCheck(cs, "k1", "git@example.test:o/r.git", "/x")
	msg := read()
	if msg["type"] != "device.clone.checked" || msg["requestId"] != "k1" || msg["reason"] != "no-origin" || msg["cloneInto"] != "/x" {
		t.Fatalf("without a hook: %#v", msg)
	}
	c.hooks.CloneCheck = func(ctx context.Context, origin, cloneInto string) CloneChecked {
		if origin != "git@example.test:o/r.git" || cloneInto != "" {
			t.Errorf("hook got %q %q", origin, cloneInto)
		}
		return CloneChecked{Reason: "destination-taken", Detail: "exists", CloneInto: "/home/me/r"}
	}
	c.handleCloneCheck(cs, "k2", "git@example.test:o/r.git", "")
	msg = read()
	if msg["reason"] != "destination-taken" || msg["detail"] != "exists" || msg["cloneInto"] != "/home/me/r" || msg["v"] != float64(ProtocolVersion) {
		t.Fatalf("with a hook: %#v", msg)
	}
}

func TestHandleCloneStartReportsProgressThenDone(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.CloneStart = func(ctx context.Context, handoffID, origin, cloneInto string, progress func(int64, *int64)) (string, error) {
		if handoffID != "h1" || origin != "o" || cloneInto != "/x" {
			t.Errorf("hook got %q %q %q", handoffID, origin, cloneInto)
		}
		total := int64(100)
		progress(10, nil)
		progress(90, &total)
		return "/x", nil
	}
	ctx := c.beginClone(cs, "c1")
	c.handleCloneStart(ctx, cs, "c1", "h1", "o", "/x")
	if msg := read(); msg["type"] != "device.clone.progress" || msg["received"] != float64(10) || msg["total"] != nil {
		t.Fatalf("first progress: %#v", msg)
	}
	if msg := read(); msg["received"] != float64(90) || msg["total"] != float64(100) {
		t.Fatalf("second progress: %#v", msg)
	}
	if msg := read(); msg["type"] != "device.clone.done" || msg["requestId"] != "c1" || msg["path"] != "/x" {
		t.Fatalf("done: %#v", msg)
	}
	c.cloneMu.Lock()
	left := len(c.clones)
	c.cloneMu.Unlock()
	if left != 0 {
		t.Fatalf("%d clones still registered", left)
	}
}

func TestHandleCloneStartFailsWithTheReasonOfTheError(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.CloneStart = func(context.Context, string, string, string, func(int64, *int64)) (string, error) {
		return "", &CloneError{Reason: "git-failed", Detail: "fatal: nope"}
	}
	c.handleCloneStart(c.beginClone(cs, "c2"), cs, "c2", "h", "o", "/x")
	if msg := read(); msg["type"] != "device.clone.failed" || msg["reason"] != "git-failed" || msg["detail"] != "fatal: nope" {
		t.Fatalf("got %#v", msg)
	}
	c.hooks.CloneStart = func(context.Context, string, string, string, func(int64, *int64)) (string, error) {
		return "", errors.New("boom")
	}
	c.handleCloneStart(c.beginClone(cs, "c3"), cs, "c3", "h", "o", "/x")
	if msg := read(); msg["reason"] != "git-failed" || msg["detail"] != nil {
		t.Fatalf("a plain error: %#v", msg)
	}
}

func TestACancelEndsTheCloneItNamesAndAnswersFailed(t *testing.T) {
	c, cs, read := dialTestConn(t)
	started := make(chan struct{})
	c.hooks.CloneStart = func(ctx context.Context, _, _, _ string, _ func(int64, *int64)) (string, error) {
		close(started)
		<-ctx.Done()
		return "", &CloneError{Reason: "cancelled"}
	}
	ctx := c.beginClone(cs, "c4")
	other := c.beginClone(cs, "c5")
	go c.handleCloneStart(ctx, cs, "c4", "h", "o", "/x")
	<-started
	c.cancelClone("c4")
	if msg := read(); msg["type"] != "device.clone.failed" || msg["requestId"] != "c4" || msg["reason"] != "cancelled" {
		t.Fatalf("got %#v", msg)
	}
	if other.Err() != nil {
		t.Fatal("a cancel of one clone ended another")
	}
	c.cancelClone("unknown") // a cancel that arrives after the end is ignored
}

func TestAClosedConnectionEndsItsClones(t *testing.T) {
	c, cs, _ := dialTestConn(t)
	ctx := c.beginClone(cs, "c6")
	close(cs.stop)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the clone outlived its connection")
	}
}

func dialTestConn(t *testing.T) (*Client, *connState, func() map[string]any) {
	t.Helper()
	up := websocket.Upgrader{}
	incoming := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			incoming <- data
		}
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cs := newConnState(conn, make(chan reply, 1))
	c := NewClient(&config.Config{ServerURL: "http://127.0.0.1", Token: "t"}, "dev", "test", Hooks{})
	read := func() map[string]any {
		select {
		case data := <-incoming:
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatal(err)
			}
			return msg
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for sync reply")
			return nil
		}
	}
	return c, cs, read
}

func TestHandleWorkspaceListing(t *testing.T) {
	c, cs, read := dialTestConn(t)
	two := 2
	c.hooks.Workspace = func(ctx context.Context, sessionID string) (WorkspaceListing, error) {
		return WorkspaceListing{Git: true, Commit: "abc", Paths: []string{"a.go"}, Changes: []WorkspaceChange{
			{Path: "a.go", Status: "modified", Added: &two, Removed: new(int)},
			{Path: "b.png", Status: "untracked", Binary: true},
		}}, nil
	}
	c.handleWorkspace(cs, "s1", "r1")
	msg := read()
	changes, _ := msg["changes"].([]any)
	if msg["type"] != "session.workspace" || msg["git"] != true || len(changes) != 2 {
		t.Fatalf("got %#v", msg)
	}
	first, second := changes[0].(map[string]any), changes[1].(map[string]any)
	if first["added"] != 2.0 || first["removed"] != 0.0 || first["binary"] != nil {
		t.Fatalf("first %#v", first)
	}
	if second["binary"] != true || second["added"] != nil {
		t.Fatalf("second %#v", second)
	}

	c.hooks.Workspace = func(ctx context.Context, sessionID string) (WorkspaceListing, error) {
		return WorkspaceListing{}, nil
	}
	c.handleWorkspace(cs, "s1", "r2")
	msg = read()
	if msg["git"] != false || msg["changes"] == nil {
		t.Fatalf("no repository: %#v", msg)
	}
}

func TestHandleWorkspaceFileImage(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.File = func(ctx context.Context, sessionID, path, at string) (WorkspaceFileContent, error) {
		if path != "logo.png" || at != "head" {
			t.Fatalf("path=%q at=%q", path, at)
		}
		return WorkspaceFileContent{Kind: "image", Size: 3, MediaType: "image/png", Data: []byte{1, 2, 3}}, nil
	}
	c.handleWorkspaceFile(cs, "s1", "r1", "logo.png", "head")
	msg := read()
	if msg["type"] != "session.workspace.file" || msg["kind"] != "image" || msg["data"] != "AQID" ||
		msg["mediaType"] != "image/png" || msg["size"] != 3.0 || msg["text"] != nil {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleWorkspaceDiff(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.Diff = func(ctx context.Context, sessionID, path string, page WorkspaceDiffPage) (WorkspaceDiffContent, error) {
		if path == "gone" {
			return WorkspaceDiffContent{}, errors.New("not-changed")
		}
		if path == "big.txt" {
			next := page.Cursor + 10
			if !page.Base || page.Limit != 2048 {
				t.Fatalf("page %#v", page)
			}
			return WorkspaceDiffContent{Status: "modified", Kind: "text", Diff: "@@\n", Truncated: true, Next: &next, Size: 99}, nil
		}
		size := int64(9)
		return WorkspaceDiffContent{Status: "added", Kind: "binary", NewSize: &size}, nil
	}
	c.handleWorkspaceDiff(cs, "s1", "r1", "pic.bin", WorkspaceDiffPage{})
	msg := read()
	if msg["type"] != "session.workspace.diff" || msg["kind"] != "binary" || msg["newSize"] != 9.0 ||
		msg["oldSize"] != nil || msg["status"] != "added" {
		t.Fatalf("got %#v", msg)
	}
	c.handleWorkspaceDiff(cs, "s1", "r2", "gone", WorkspaceDiffPage{})
	msg = read()
	if msg["type"] != "session.workspace.rejected" || msg["reason"] != "not-changed" || msg["requestId"] != "r2" {
		t.Fatalf("got %#v", msg)
	}
	c.handleWorkspaceDiff(cs, "s1", "r3", "big.txt", WorkspaceDiffPage{Base: true, Cursor: 30, Limit: 2048})
	msg = read()
	if msg["type"] != "session.workspace.diff" || msg["truncated"] != true || msg["next"] != 40.0 || msg["size"] != 99.0 {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleWorkspaceChanges(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.hooks.Changes = func(ctx context.Context, sessionID string) (WorkspaceChangesContent, error) {
		if sessionID == "s2" {
			return WorkspaceChangesContent{}, errors.New("not-a-git-repository")
		}
		if sessionID == "s3" {
			return WorkspaceChangesContent{Branch: "b", Base: "abc", Shared: true}, nil
		}
		added, removed := 3, 1
		return WorkspaceChangesContent{Branch: "hoplet/x", Base: "abc", Total: 201, Files: []WorkspaceChange{
			{Path: "a.go", Status: "modified", Added: &added, Removed: &removed},
		}}, nil
	}
	c.handleWorkspaceChanges(cs, "s1", "r1")
	msg := read()
	files, _ := msg["files"].([]any)
	if msg["type"] != "session.workspace.changes" || msg["branch"] != "hoplet/x" || msg["base"] != "abc" ||
		msg["total"] != 201.0 || msg["shared"] != nil || len(files) != 1 {
		t.Fatalf("got %#v", msg)
	}
	c.handleWorkspaceChanges(cs, "s3", "r3")
	msg = read()
	if empty, ok := msg["files"].([]any); !ok || len(empty) != 0 || msg["shared"] != true {
		t.Fatalf("got %#v", msg)
	}
	c.handleWorkspaceChanges(cs, "s2", "r2")
	msg = read()
	if msg["type"] != "session.workspace.rejected" || msg["reason"] != "not-a-git-repository" {
		t.Fatalf("got %#v", msg)
	}
}

func TestHandleConfigRead(t *testing.T) {
	c, cs, read := dialTestConn(t)
	next := int64(32768)
	c.hooks.ConfigRead = func(ctx context.Context, req SessionConfigRead) (SessionConfig, error) {
		switch req.Name {
		case "refused":
			return SessionConfig{}, &ConfigError{Code: "invalid", Reason: "invalid name"}
		case "broken":
			return SessionConfig{}, errors.New("disk on fire")
		}
		return SessionConfig{Exists: true, Size: 40000, Hash: "abc", Offset: req.Offset, Text: "hi", NextOffset: &next}, nil
	}
	ask := func(name string) {
		raw, _ := json.Marshal(SessionConfigRead{
			V: ProtocolVersion, Type: TypeSessionConfigRead, SessionID: "s1", RequestID: "r1",
			Provider: "claude", Kind: "skill", Scope: "project", Name: name, Offset: 5,
		})
		c.handleConfigRead(cs, raw)
	}
	ask("review")
	msg := read()
	if msg["type"] != "session.config" || msg["requestId"] != "r1" || msg["kind"] != "skill" || msg["name"] != "review" ||
		msg["exists"] != true || msg["offset"] != 5.0 || msg["nextOffset"] != 32768.0 || msg["text"] != "hi" {
		t.Fatalf("got %#v", msg)
	}
	if entries, ok := msg["entries"].([]any); !ok || len(entries) != 0 {
		t.Fatalf("entries %#v", msg["entries"])
	}
	ask("refused")
	msg = read()
	if msg["type"] != "session.config.rejected" || msg["code"] != "invalid" || msg["reason"] != "invalid name" {
		t.Fatalf("got %#v", msg)
	}
	ask("broken")
	msg = read()
	if msg["type"] != "session.config.rejected" || msg["code"] != "io" {
		t.Fatalf("got %#v", msg)
	}
	c.hooks.ConfigRead = nil
	ask("review")
	msg = read()
	if msg["type"] != "session.config.rejected" || msg["code"] != "unsupported" {
		t.Fatalf("got %#v", msg)
	}
}

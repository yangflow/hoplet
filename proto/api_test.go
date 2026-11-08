package proto

import (
	"encoding/json"
	"testing"
	"time"
)

// TestFrameWireFormat pins the JSON shape of protocol frames: every client
// depends on it.
func TestFrameWireFormat(t *testing.T) {
	archived := time.Unix(1, 0).UTC()
	for name, tc := range map[string]struct {
		v    any
		want string
	}{
		"request":     {ClientFrame{ID: 1, Method: MethodHello, Params: json.RawMessage(`{"token":"t"}`)}, `{"id":1,"method":"hello","params":{"token":"t"}}`},
		"cancel":      {ClientFrame{Sub: 4, Cancel: true}, `{"sub":4,"cancel":true}`},
		"result":      {ServerFrame{ID: 1, Result: json.RawMessage(`{}`)}, `{"id":1,"result":{}}`},
		"error":       {ServerFrame{ID: 2, Error: &APIError{Code: CodeBusy, Message: "m"}}, `{"id":2,"error":{"code":"busy","message":"m"}}`},
		"item":        {ServerFrame{Sub: 3, Item: json.RawMessage(`{"events":[]}`)}, `{"sub":3,"item":{"events":[]}}`},
		"end":         {ServerFrame{Sub: 3, End: true}, `{"sub":3,"end":true}`},
		"queued":      {SendReply{Queued: true}, `{"queued":true}`},
		"queued msg":  {QueuedMessage{Key: "k", Text: "t", Position: 1, CreatedAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)}, `{"key":"k","text":"t","position":1,"created_at":"2026-11-08T00:00:00Z"}`},
		"report msg":  {QueuedMessage{Key: "k", Text: "t", CreatedAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), From: "c1"}, `{"key":"k","text":"t","position":0,"created_at":"2026-11-08T00:00:00Z","from":"c1"}`},
		"message":     {Message{ID: "m", Role: RoleUser, Text: "t"}, `{"id":"m","role":"user","text":"t"}`},
		"report":      {Message{ID: "m", Role: RoleUser, Text: "t", From: "c1"}, `{"id":"m","role":"user","text":"t","from":"c1"}`},
		"recorded":    {SendReply{Seq: 7, Turn: 2}, `{"seq":7,"turn":2}`},
		"empty queue": {SessionItem{Queue: &Queue{Messages: []QueuedMessage{}}}, `{"queue":{"messages":[]}}`},
		"events page": {EventsPage{Events: []Event{}, More: true}, `{"events":[],"more":true}`},
		"diff args":   {DiffParams{Session: "s", Repo: "api", Patch: true}, `{"session":"s","repo":"api","patch":true}`},
		"diff range":  {DiffParams{Session: "s", Repo: "api", Range: DiffRangeCommit, Commit: "abc"}, `{"session":"s","repo":"api","range":"commit","commit":"abc"}`},
		"commits":     {SessionCommits{Session: "s", Repos: []RepoCommits{{Path: ".", Base: "main", Commits: []Commit{{SHA: "abc", Subject: "fix"}}, Truncated: true}}}, `{"session":"s","repos":[{"path":".","base":"main","commits":[{"sha":"abc","subject":"fix"}],"truncated":true}]}`},
		"diff none":   {SessionDiff{Session: "s", Repos: []RepoDiff{}}, `{"session":"s","repos":[]}`},
		"repo diff": {RepoDiff{Path: ".", State: RepoNew, Files: []FileDiff{{Path: "a", Status: FileStatusAdded, Additions: 2}}, Skipped: []SkippedFile{{Path: "big", Size: 9, Reason: "too large"}}, SHA: "h"},
			`{"path":".","state":"new","files":[{"path":"a","status":"added","additions":2,"deletions":0}],"skipped":[{"path":"big","size":9,"reason":"too large"}],"sha":"h"}`},
		"renamed binary": {FileDiff{Path: "b", OldPath: "a", Status: FileStatusRenamed, Binary: true, Filter: "lfs"},
			`{"path":"b","old_path":"a","status":"renamed","additions":0,"deletions":0,"binary":true,"filter":"lfs"}`},
		"events args": {EventsParams{Session: "s", BeforeSeq: 11, Limit: 5}, `{"session":"s","before_seq":11,"limit":5}`},
		"list args":   {FilesListParams{Session: "s", Path: "internal", Ignored: true}, `{"session":"s","path":"internal","ignored":true}`},
		"list all":    {FilesListParams{Session: "s", All: true}, `{"session":"s","all":true}`},
		"list empty":  {FileList{Session: "s", Entries: []FileEntry{}}, `{"session":"s","path":"","entries":[]}`},
		"listing": {FileList{Session: "s", Path: "a", Entries: []FileEntry{{Path: "a/b", Kind: FileKindDir}, {Path: "a/c.go", Kind: FileKindFile, Size: 12}, {Path: "a/l", Kind: FileKindSymlink, Ignored: true}}, Truncated: true},
			`{"session":"s","path":"a","entries":[{"path":"a/b","kind":"dir"},{"path":"a/c.go","kind":"file","size":12},{"path":"a/l","kind":"symlink","ignored":true}],"truncated":true}`},
		"read args":   {FilesReadParams{Session: "s", Path: "a.go", Baseline: true}, `{"session":"s","path":"a.go","baseline":true}`},
		"read text":   {FileContent{Session: "s", Path: "a.go", Text: "x", Size: 1}, `{"session":"s","path":"a.go","text":"x","size":1}`},
		"read binary": {FileContent{Session: "s", Path: "i.png", Size: 9, Binary: true}, `{"session":"s","path":"i.png","size":9,"binary":true}`},
		"read large":  {FileContent{Session: "s", Path: "big", Size: 9e6, TooLarge: true}, `{"session":"s","path":"big","size":9000000,"too_large":true}`},
		"read gone":   {FileContent{Session: "s", Path: "x", Missing: true}, `{"session":"s","path":"x","missing":true}`},
		"tool hello":  {HelloParams{ProtocolMajor: 1, SessionToken: "k"}, `{"protocol_major":1,"device_id":"","token":"","session_token":"k"}`},
		"tools": {ToolsListResult{Tools: []ToolInfo{{Name: "session_get", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}}},
			`{"tools":[{"name":"session_get","description":"d","input_schema":{"type":"object"}}]}`},
		"tool call":   {ToolsCallParams{Name: "session_get", Arguments: json.RawMessage(`{"id":"abcd"}`)}, `{"name":"session_get","arguments":{"id":"abcd"}}`},
		"tool result": {ToolsCallResult{Text: "no such session", IsError: true}, `{"text":"no such session","is_error":true}`},
		"companion": {Companion{ID: "c1", Name: "Ada", Host: "h", Harness: HarnessRef{Kind: "claude"}, Main: "s1", Dir: "/c/c1", CreatedAt: time.Unix(0, 0).UTC(), ArchivedAt: &archived},
			`{"id":"c1","name":"Ada","host":"h","harness":{"kind":"claude"},"main":"s1","dir":"/c/c1","created_at":"1970-01-01T00:00:00Z","archived_at":"1970-01-01T00:00:01Z"}`},
		"companion ref":   {CompanionRef{ID: "c1"}, `{"id":"c1"}`},
		"companions all":  {CompanionsListParams{All: true}, `{"all":true}`},
		"no companions":   {CompanionsList{Companions: []Companion{}}, `{"companions":[]}`},
		"companion new":   {CompanionCreateParams{Harness: "hermes", Permission: ModeConfirm, Title: "Ada", Model: "m"}, `{"harness":"hermes","permission":"confirm","title":"Ada","model":"m"}`},
		"persona":         {Persona{ID: "c1", Text: "be kind"}, `{"id":"c1","text":"be kind"}`},
		"persona applies": {SetPersonaResult{Applies: AppliesNewMainSession}, `{"applies":"new_main_session"}`},
		"artifact": {Artifact{ID: "a1", Session: "s1", Name: "report", Kind: ArtifactHTML, Version: 2, Entry: "index.html", By: ArtifactByCLI, Files: 3, Size: 42, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()},
			`{"id":"a1","session":"s1","name":"report","kind":"html","version":2,"entry":"index.html","by":"cli","files":3,"size":42,"created_at":"1970-01-01T00:00:00Z","updated_at":"1970-01-01T00:00:01Z"}`},
		"artifact publish": {ArtifactsPublishParams{Session: "s1", Name: "report", Entry: "/w/index.html", Files: []string{"/w/app.js"}},
			`{"session":"s1","name":"report","entry":"/w/index.html","files":["/w/app.js"]}`},
		"artifact list":  {ArtifactsListParams{Session: "s1"}, `{"session":"s1"}`},
		"no artifacts":   {ArtifactsList{Artifacts: []Artifact{}}, `{"artifacts":[]}`},
		"artifact read":  {ArtifactsReadParams{Artifact: "a1", Version: 1, Path: "app.js", Offset: 1048576}, `{"artifact":"a1","version":1,"path":"app.js","offset":1048576}`},
		"artifact entry": {ArtifactsReadParams{Artifact: "a1"}, `{"artifact":"a1"}`},
		"artifact chunk": {ArtifactChunk{Data: []byte("hi"), Size: 2, ContentType: "text/html; charset=utf-8"}, `{"data":"aGk=","size":2,"content_type":"text/html; charset=utf-8"}`},
	} {
		b, err := json.Marshal(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("%s: %s, want %s", name, b, tc.want)
		}
	}
	if got := (&APIError{Code: CodeNotFound, Message: "no session"}).Error(); got != "not_found: no session" {
		t.Errorf("error text %q", got)
	}
}

// TestFrameRoundTrip checks that frames survive a decode and re-encode with
// identical bytes, and that a request's params decode into their type.
func TestFrameRoundTrip(t *testing.T) {
	req := ClientFrame{ID: 1, Method: MethodSessionsSend, Params: json.RawMessage(`{"session":"s","text":"t","key":"k"}`)}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back ClientFrame
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(b) {
		t.Fatalf("client frame re-encoded as %s, want %s", again, b)
	}
	var sp SendParams
	if err := json.Unmarshal(back.Params, &sp); err != nil {
		t.Fatal(err)
	}
	if sp.Session != "s" || sp.Text != "t" || sp.Key != "k" {
		t.Fatalf("send params %+v", sp)
	}

	for name, v := range map[string]any{
		"cancel": &ClientFrame{Sub: 2, Cancel: true},
		"error":  &ServerFrame{ID: 3, Error: &APIError{Code: CodeConflict, Message: "m"}},
		"item":   &ServerFrame{Sub: 4, Item: json.RawMessage(`{"events":[]}`), End: false},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		switch v.(type) {
		case *ClientFrame:
			out = new(ClientFrame)
		default:
			out = new(ServerFrame)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(b) {
			t.Errorf("%s re-encoded as %s, want %s", name, again, b)
		}
	}
}

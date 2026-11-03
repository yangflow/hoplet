package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestListProperties(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sess := newSession(t, s, "s")
	if sess.Pinned || sess.Unread || sess.Preview != "" || sess.Title != "" {
		t.Fatalf("new session %+v", sess)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.Message{ID: "u1", Role: proto.RoleUser, Text: "\n  fix   the\tsync bug\nin the bridge"}},
	}})
	got, _ := s.Session(ctx, "s")
	if got.Title != "fix the sync bug" || got.Preview != "fix the sync bug" || got.Unread {
		t.Fatalf("after my message %+v", got)
	}
	long := strings.Repeat("长", 200)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.Message{ID: "a1", Role: proto.RoleAssistant, Text: long}},
		{Payload: proto.Message{ID: "a2", Role: proto.RoleAssistant, Text: " \n "}},
		{Payload: proto.TurnEnded{Reason: proto.TurnCompleted}},
	}})
	got, _ = s.Session(ctx, "s")
	if got.Title != "fix the sync bug" || got.Preview != strings.Repeat("长", 120) || !got.Unread {
		t.Fatalf("after the turn %+v", got)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 2, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.Message{ID: "u2", Role: proto.RoleUser, Text: "and the tests"}},
	}})
	if got, _ = s.Session(ctx, "s"); got.Title != "fix the sync bug" || got.Preview != "and the tests" {
		t.Fatalf("a later message renamed the session: %+v", got)
	}

	before := got
	pin, read := true, false
	got, err := s.UpdateSession(ctx, "s", SessionUpdate{Pinned: &pin, Unread: &read})
	if err != nil || !got.Pinned || got.Unread {
		t.Fatalf("update %+v %v", got, err)
	}
	if !got.UpdatedAt.Equal(before.UpdatedAt) || got.Version <= before.Version {
		t.Fatalf("update moved updated_at or kept the version: %+v, before %+v", got, before)
	}
}

func TestFirstLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 10, ""},
		{" \n\t\n", 10, ""},
		{"one", 10, "one"},
		{"  a  b \r\nc", 10, "a b"},
		{"\n\nsecond line", 20, "second line"},
		{"abcdef", 3, "abc"},
		{"你好世界", 2, "你好"},
	} {
		if got := firstLine(tc.in, tc.n); got != tc.want {
			t.Errorf("firstLine(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestModelChanged(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.ModelChanged{Model: "gpt-6.1-sol", Provider: "local"}},
	}})
	got, _ := s.Session(ctx, "s")
	if got.Model != "gpt-6.1-sol" || got.ModelProvider != "local" {
		t.Fatalf("after model.changed %+v", got)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.ModelChanged{Model: "claude-opus-5-5"}}}})
	if got, _ = s.Session(ctx, "s"); got.Model != "claude-opus-5-5" || got.ModelProvider != "" {
		t.Fatalf("a model without a provider kept the old one: %+v", got)
	}
}

func TestToolOutput(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	ref, err := s.PutBlob(strings.NewReader("line 1\nline 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "ls", Status: proto.ToolRunning}},
		{Payload: proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "ls", Status: proto.ToolDone, Output: &ref}},
		{Payload: proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "pwd", Status: proto.ToolDone}},
	}})
	got, err := s.ToolOutput(ctx, "s", "t1")
	if err != nil || got != ref {
		t.Fatalf("output of t1: %+v %v, want %+v", got, err, ref)
	}
	if _, err := s.ToolOutput(ctx, "s", "t2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a call without output: %v", err)
	}
	m := "claude-opus-5-5"
	if got, err := s.UpdateSession(ctx, "s", SessionUpdate{Model: &m}); err != nil || got.Model != m {
		t.Fatalf("update model %+v %v", got, err)
	}
}

package store

import (
	"context"
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

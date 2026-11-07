package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

type fakeSource struct {
	sessions []proto.Session
	err      error
}

func (f fakeSource) Sessions(context.Context) ([]proto.Session, error) { return f.sessions, f.err }

func (f fakeSource) State(_ context.Context, id proto.SessionID) (proto.SessionState, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return proto.SessionState{Session: s, Pending: []proto.ApprovalRequested{}, Queue: []proto.QueuedMessage{}}, nil
		}
	}
	return proto.SessionState{}, fmt.Errorf("%w: session %s", store.ErrNotFound, id)
}

func TestTools(t *testing.T) {
	archived := time.Unix(1, 0)
	src := fakeSource{sessions: []proto.Session{
		{ID: "abcd1111", Title: "one"},
		{ID: "abcd2222", Title: "two"},
		{ID: "efgh3333", Title: "gone", ArchivedAt: &archived},
	}}
	set := New(src)
	caller := proto.Session{ID: "abcd1111", Mode: proto.ModeCode}
	var names []string
	for _, ti := range set.List(caller) {
		names = append(names, ti.Name)
		if !json.Valid(ti.InputSchema) {
			t.Errorf("%s: schema is not JSON", ti.Name)
		}
	}
	if got := strings.Join(names, ","); got != "sessions_list,session_get" {
		t.Fatalf("tools %s", got)
	}

	for _, tc := range []struct {
		name, args string
		want       string
		isErr      bool
	}{
		{"sessions_list", ``, `"abcd2222"`, false},
		{"sessions_list", `{"x":1}`, "bad arguments", true},
		{"session_get", `{"id":"abcd2"}`, `"title":"two"`, false},
		{"session_get", `{"id":"abcd2222"}`, `"queue":[]`, false},
		{"session_get", `{"id":"abcd"}`, "matches 2 sessions", true},
		{"session_get", `{"id":"abc"}`, "at least 4", true},
		{"session_get", `{"id":"zzzz"}`, "no session zzzz", true},
		{"session_get", `{}`, "at least 4", true},
		{"session_get", `[1]`, "bad arguments", true},
		{"nope", `{}`, "no tool named nope", true},
	} {
		r, err := set.Call(context.Background(), caller, tc.name, json.RawMessage(tc.args))
		if err != nil || r.IsError != tc.isErr || !strings.Contains(r.Text, tc.want) {
			t.Errorf("%s %s: %+v %v", tc.name, tc.args, r, err)
		}
	}
	r, _ := set.Call(context.Background(), caller, "sessions_list", nil)
	if strings.Contains(r.Text, "efgh3333") {
		t.Errorf("archived session listed: %s", r.Text)
	}

	// A failure of the services is a request error, not a tool result.
	broken := New(fakeSource{err: errors.New("disk on fire")})
	if _, err := broken.Call(context.Background(), caller, "sessions_list", nil); err == nil {
		t.Error("service failure became a tool result")
	}
}

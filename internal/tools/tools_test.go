package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/artifact"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

type fakeSource struct {
	sessions []proto.Session
	err      error
	workDir  string
	got      *changes // what the tools asked to change
}

// changes records the calls that change something.
type changes struct {
	creates   []Create
	sends     []proto.SendParams
	reports   []report
	publishes []artifact.Request
	failure   error // returned by every change
}

type report struct {
	parent, from proto.SessionID
	key, text    string
}

func (f fakeSource) Create(_ context.Context, c Create) (proto.Session, error) {
	f.got.creates = append(f.got.creates, c)
	if f.got.failure != nil {
		return proto.Session{}, f.got.failure
	}
	return proto.Session{ID: "new00001", Mode: c.Mode, Parent: c.Parent, Owner: c.Owner}, nil
}

func (f fakeSource) Send(_ context.Context, p proto.SendParams) (proto.SendReply, error) {
	f.got.sends = append(f.got.sends, p)
	return proto.SendReply{Queued: true}, f.got.failure
}

func (f fakeSource) Report(_ context.Context, parent, from proto.SessionID, key, text string) error {
	f.got.reports = append(f.got.reports, report{parent, from, key, text})
	return f.got.failure
}

func (f fakeSource) WorkDir(context.Context, proto.SessionID) (string, error) { return f.workDir, nil }

func (f fakeSource) Publish(_ context.Context, r artifact.Request) (proto.Artifact, error) {
	f.got.publishes = append(f.got.publishes, r)
	return proto.Artifact{ID: "art1", Name: r.Name, Kind: proto.ArtifactHTML, Version: 2}, f.got.failure
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
	src.got = &changes{}
	set := New(src)
	caller := proto.Session{ID: "abcd1111", Mode: proto.ModeCode}
	var names []string
	for _, ti := range set.List(caller) {
		names = append(names, ti.Name)
		if !json.Valid(ti.InputSchema) {
			t.Errorf("%s: schema is not JSON", ti.Name)
		}
	}
	if got := strings.Join(names, ","); got != "sessions_list,session_get,artifact_publish" {
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

func TestImageResult(t *testing.T) {
	img := proto.ToolImage{MIME: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}
	var failErr error
	set := &Set{tools: []tool{{
		info:    proto.ToolInfo{Name: "shot"},
		visible: func(proto.Session) bool { return true },
		call: func(context.Context, proto.Session, json.RawMessage) (any, error) {
			return imageResult{Value: map[string]int{"width": 2}, Image: img}, failErr
		},
	}}}
	caller := proto.Session{ID: "abcd1111"}

	r, err := set.Call(context.Background(), caller, "shot", nil)
	if err != nil || r.IsError || r.Text != `{"width":2}` || len(r.Images) != 1 || r.Images[0].MIME != "image/png" || string(r.Images[0].Data) != string(img.Data) {
		t.Fatalf("image result = %+v, %v", r, err)
	}

	failErr = failf("device is busy")
	r, err = set.Call(context.Background(), caller, "shot", nil)
	if err != nil || !r.IsError || r.Text != "device is busy" || r.Images != nil {
		t.Fatalf("failure = %+v, %v", r, err)
	}

	// A plain value carries no images.
	r, err = New(fakeSource{}).Call(context.Background(), caller, "sessions_list", nil)
	if err != nil || r.Images != nil {
		t.Fatalf("plain result = %+v, %v", r, err)
	}
}

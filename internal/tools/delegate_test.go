package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func names(set *Set, caller proto.Session) []string {
	var out []string
	for _, ti := range set.List(caller) {
		out = append(out, ti.Name)
	}
	return out
}

func TestDelegationToolsAreVisibleByCaller(t *testing.T) {
	set := New(fakeSource{got: &changes{}})
	for _, tc := range []struct {
		name   string
		caller proto.Session
		want   []string
	}{
		{"code", proto.Session{Mode: proto.ModeCode}, []string{"sessions_list", "session_get"}},
		{"handed off", proto.Session{Mode: proto.ModeCode, Parent: "p"}, []string{"sessions_list", "session_get", "report"}},
		{"companion", proto.Session{Mode: proto.ModePersonal}, []string{"sessions_list", "session_get", "session_create", "session_send", "handoff"}},
		{"child", proto.Session{Mode: proto.ModePersonal, Parent: "p"}, []string{"sessions_list", "session_get", "session_create", "session_send", "handoff", "report"}},
	} {
		if got := names(set, tc.caller); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	for _, ti := range set.List(proto.Session{Mode: proto.ModePersonal, Parent: "p"}) {
		if !json.Valid(ti.InputSchema) {
			t.Errorf("%s: schema is not JSON", ti.Name)
		}
	}
	// Hidden tools cannot be called either.
	code := proto.Session{ID: "code0001", Mode: proto.ModeCode}
	for _, name := range []string{"session_create", "session_send", "handoff", "report"} {
		r, err := set.Call(context.Background(), code, name, json.RawMessage(`{}`))
		if err != nil || !r.IsError || !strings.Contains(r.Text, "no tool named") {
			t.Errorf("%s from a code session: %+v %v", name, r, err)
		}
	}
}

func TestSessionCreate(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	caller := proto.Session{ID: "comp0001", Mode: proto.ModePersonal, Harness: proto.HarnessRef{Kind: "claude"},
		Permission: proto.ModeAutoEdit, Model: "m1", ModelProvider: "p1", Effort: proto.EffortHigh}
	for _, tc := range []struct {
		name, args string
		want       Create
		fails      string
	}{
		{"same harness", `{"prompt":"look it up","title":"t"}`, Create{Mode: proto.ModePersonal, Harness: "claude", Title: "t",
			Owner: proto.OwnerDelegated, Parent: "comp0001", Permission: proto.ModeAutoEdit, Model: "m1", ModelProvider: "p1", Effort: proto.EffortHigh, Prompt: "look it up"}, ""},
		{"named same harness", `{"prompt":"x","harness":"claude","directory":"` + dir + `"}`, Create{Mode: proto.ModePersonal, Harness: "claude", Directory: dir,
			Owner: proto.OwnerDelegated, Parent: "comp0001", Permission: proto.ModeAutoEdit, Model: "m1", ModelProvider: "p1", Effort: proto.EffortHigh, Prompt: "x"}, ""},
		{"other harness", `{"prompt":"x","harness":"hermes"}`, Create{Mode: proto.ModePersonal, Harness: "hermes",
			Owner: proto.OwnerDelegated, Parent: "comp0001", Permission: proto.ModeAutoEdit, Prompt: "x"}, ""},
		{"no prompt", `{"title":"t"}`, Create{}, "prompt is required"},
		{"relative dir", `{"prompt":"x","directory":"work"}`, Create{}, "absolute"},
		{"missing dir", `{"prompt":"x","directory":"` + filepath.Join(dir, "nope") + `"}`, Create{}, "no such file"},
		{"file as dir", `{"prompt":"x","directory":"` + file + `"}`, Create{}, "not a directory"},
		{"unknown field", `{"prompt":"x","permission":"full_auto"}`, Create{}, "bad arguments"},
	} {
		got := &changes{}
		r, err := New(fakeSource{got: got}).Call(context.Background(), caller, "session_create", json.RawMessage(tc.args))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.fails != "" {
			if !r.IsError || !strings.Contains(r.Text, tc.fails) || len(got.creates) != 0 {
				t.Errorf("%s: %+v, creates %v", tc.name, r, got.creates)
			}
			continue
		}
		if r.IsError || len(got.creates) != 1 || got.creates[0] != tc.want || !strings.Contains(r.Text, `"id":"new00001"`) {
			t.Errorf("%s: %+v, creates %+v", tc.name, r, got.creates)
		}
	}
}

func TestHandoff(t *testing.T) {
	dir := t.TempDir()
	caller := proto.Session{ID: "comp0001", Mode: proto.ModePersonal, Harness: proto.HarnessRef{Kind: "codex"}, Permission: proto.ModeFullAuto, Model: "m1"}
	got := &changes{}
	set := New(fakeSource{got: got})
	r, err := set.Call(context.Background(), caller, "handoff", json.RawMessage(`{"directory":"`+dir+`","summary":"fix the bug","title":"bug"}`))
	want := Create{Mode: proto.ModeCode, Harness: "codex", Directory: dir, Title: "bug", Owner: proto.OwnerMe, Parent: "comp0001", Permission: proto.ModeConfirm, Prompt: "fix the bug"}
	if err != nil || r.IsError || len(got.creates) != 1 || got.creates[0] != want {
		t.Fatalf("handoff %+v %v, creates %+v", r, err, got.creates)
	}
	for args, fails := range map[string]string{
		`{"summary":"x"}`:                 "directory is required",
		`{"directory":"` + dir + `"}`:     "summary is required",
		`{"directory":"x","summary":"x"}`: "absolute",
	} {
		r, err := set.Call(context.Background(), caller, "handoff", json.RawMessage(args))
		if err != nil || !r.IsError || !strings.Contains(r.Text, fails) {
			t.Errorf("%s: %+v %v", args, r, err)
		}
	}
	if len(got.creates) != 1 {
		t.Errorf("creates %+v", got.creates)
	}
	// A session stored whose first message failed is named in the failure.
	got.failure = errors.New("harness gone")
	r, err = set.Call(context.Background(), caller, "handoff", json.RawMessage(`{"directory":"`+dir+`","summary":"x"}`))
	if err == nil || r.IsError {
		t.Fatalf("service failure: %+v %v", r, err)
	}
}

func TestSessionSendOnlyToOwnChildren(t *testing.T) {
	caller := proto.Session{ID: "comp0001", Mode: proto.ModePersonal}
	got := &changes{}
	set := New(fakeSource{got: got, sessions: []proto.Session{
		caller,
		{ID: "kid00001", Mode: proto.ModePersonal, Owner: proto.OwnerDelegated, Parent: "comp0001"},
		{ID: "other001", Mode: proto.ModePersonal, Owner: proto.OwnerDelegated, Parent: "comp0002"},
		{ID: "mine0001", Mode: proto.ModePersonal, Owner: proto.OwnerMe},
		{ID: "hand0001", Mode: proto.ModeCode, Owner: proto.OwnerMe, Parent: "comp0001"},
		{ID: "gkid0001", Mode: proto.ModePersonal, Owner: proto.OwnerDelegated, Parent: "kid00001"},
	}})
	r, err := set.Call(context.Background(), caller, "session_send", json.RawMessage(`{"id":"kid0","text":"and also"}`))
	if err != nil || r.IsError || r.Text != `{"queued":true}` || len(got.sends) != 1 {
		t.Fatalf("send %+v %v %+v", r, err, got.sends)
	}
	if s := got.sends[0]; s.Session != "kid00001" || s.Text != "and also" || !strings.HasPrefix(s.Key, "send:comp0001:") {
		t.Fatalf("sent %+v", s)
	}
	for _, id := range []string{"other001", "mine0001", "hand0001", "gkid0001"} {
		r, err := set.Call(context.Background(), caller, "session_send", json.RawMessage(`{"id":"`+id+`","text":"x"}`))
		if err != nil || !r.IsError || !strings.Contains(r.Text, "not started by you") {
			t.Errorf("send to %s: %+v %v", id, r, err)
		}
	}
	if r, _ := set.Call(context.Background(), caller, "session_send", json.RawMessage(`{"id":"kid00001"}`)); !r.IsError {
		t.Errorf("send without text: %+v", r)
	}
	// A child sends to its own children.
	kid := proto.Session{ID: "kid00001", Mode: proto.ModePersonal, Owner: proto.OwnerDelegated, Parent: "comp0001"}
	if r, err := set.Call(context.Background(), kid, "session_send", json.RawMessage(`{"id":"gkid","text":"x"}`)); err != nil || r.IsError {
		t.Errorf("child to grandchild: %+v %v", r, err)
	}
	if len(got.sends) != 2 {
		t.Errorf("sends %+v", got.sends)
	}
}

func TestReportGoesToTheDirectParent(t *testing.T) {
	got := &changes{}
	set := New(fakeSource{got: got})
	grandchild := proto.Session{ID: "gkid0001", Mode: proto.ModePersonal, Owner: proto.OwnerDelegated, Parent: "kid00001"}
	r, err := set.Call(context.Background(), grandchild, "report", json.RawMessage(`{"text":"done"}`))
	if err != nil || r.IsError || r.Text != `{"queued":true}` || len(got.reports) != 1 {
		t.Fatalf("report %+v %v %+v", r, err, got.reports)
	}
	if rp := got.reports[0]; rp.parent != "kid00001" || rp.from != "gkid0001" || rp.text != "done" || !strings.HasPrefix(rp.key, "report:gkid0001:") {
		t.Fatalf("reported %+v", rp)
	}
	if r, _ := set.Call(context.Background(), grandchild, "report", json.RawMessage(`{}`)); !r.IsError {
		t.Errorf("report without text: %+v", r)
	}
	// The services' failures the model can read reach it.
	got.failure = &Failure{Message: "the queue is full"}
	if r, err := set.Call(context.Background(), grandchild, "report", json.RawMessage(`{"text":"x"}`)); err != nil || !r.IsError || r.Text != "the queue is full" {
		t.Errorf("failed report: %+v %v", r, err)
	}
}

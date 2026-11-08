package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

type fakeProfile struct {
	entries []memory.Entry
	result  memory.Result
	err     error
	writes  *[]memory.Write
}

var profileScopes = []memory.Scope{{Kind: memory.ScopeGlobal}, {Kind: memory.ScopeCompanion, Value: "c1"}}

func (fakeProfile) Scopes(context.Context, proto.Session) ([]memory.Scope, error) {
	return slices.Clone(profileScopes), nil
}

func (f fakeProfile) List(_ context.Context, scopes []memory.Scope, q store.MemoryQuery) ([]memory.Entry, error) {
	var out []memory.Entry
	for _, e := range f.entries {
		if e.Status == memory.StatusActive && slices.Contains(scopes, e.Scope) && (q.Limit == 0 || len(out) < q.Limit) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f fakeProfile) Get(_ context.Context, scopes []memory.Scope, id string) (memory.Entry, error) {
	for _, e := range f.entries {
		if e.ID == id && e.Status == memory.StatusActive && slices.Contains(scopes, e.Scope) {
			return e, nil
		}
	}
	return memory.Entry{}, store.ErrNotFound
}

func (f fakeProfile) Apply(_ context.Context, w memory.Write) (memory.Result, error) {
	*f.writes = append(*f.writes, w)
	return f.result, f.err
}

func profileSet(p fakeProfile) (*Set, *[]memory.Write) {
	writes := &[]memory.Write{}
	p.writes = writes
	return New(fakeSource{got: &changes{}, profile: p}), writes
}

var (
	companionCaller = proto.Session{ID: "pers0001", Mode: proto.ModePersonal}
	active          = memory.Entry{ID: "in", Version: 3, Category: memory.CategoryFact, Status: memory.StatusActive, Scope: memory.Scope{Kind: memory.ScopeGlobal}, Body: "likes tea\nmore"}
	outside         = memory.Entry{ID: "out", Version: 1, Status: memory.StatusActive, Scope: memory.Scope{Kind: memory.ScopeCompanion, Value: "c2"}, Body: "x"}
	pending         = memory.Entry{ID: "cand", Version: 1, Status: memory.StatusCandidate, Scope: memory.Scope{Kind: memory.ScopeGlobal}, Body: "x"}
)

func TestProfileRead(t *testing.T) {
	set, _ := profileSet(fakeProfile{entries: []memory.Entry{active, outside, pending}})
	ctx := context.Background()
	r, err := set.Call(ctx, companionCaller, "profile_list", nil)
	if err != nil || r.IsError || r.Text != `[{"id":"in","version":3,"category":"fact","scope":{"kind":"global"},"title":"likes tea"}]` {
		t.Fatalf("list %+v %v", r, err)
	}
	if r, err := set.Call(ctx, companionCaller, "profile_list", json.RawMessage(`{"scope":"companion"}`)); err != nil || r.Text != `[]` {
		t.Fatalf("filtered %+v %v", r, err)
	}
	if r, err := set.Call(ctx, companionCaller, "profile_read", json.RawMessage(`{"id":"in"}`)); err != nil || r.IsError || !strings.Contains(r.Text, `"body":"likes tea\nmore"`) {
		t.Fatalf("read %+v %v", r, err)
	}
	for _, id := range []string{"out", "cand", "nope"} {
		r, err := set.Call(ctx, companionCaller, "profile_read", json.RawMessage(`{"id":"`+id+`"}`))
		if err != nil || !r.IsError || r.Text != "no entry "+id {
			t.Errorf("%s: %+v %v", id, r, err)
		}
	}
}

func TestProfileListLimit(t *testing.T) {
	var many []memory.Entry
	for i := range 250 {
		e := active
		e.ID = fmt.Sprint("e", i)
		many = append(many, e)
	}
	set, _ := profileSet(fakeProfile{entries: many})
	for args, want := range map[string]int{`{}`: 50, `{"limit":3}`: 3, `{"limit":1000}`: 200} {
		r, err := set.Call(context.Background(), companionCaller, "profile_list", json.RawMessage(args))
		var items []profileItem
		if err != nil || json.Unmarshal([]byte(r.Text), &items) != nil || len(items) != want {
			t.Errorf("%s: %d items, %v", args, len(items), err)
		}
	}
}

func TestProfileWriteIsPersonalOnly(t *testing.T) {
	set, writes := profileSet(fakeProfile{})
	code := proto.Session{ID: "code0001", Mode: proto.ModeCode}
	r, err := set.Call(context.Background(), code, "profile_write", json.RawMessage(`{"action":"create","reason":"r"}`))
	if err != nil || !r.IsError || r.Text != "profile_write is only available in Personal sessions" {
		t.Fatalf("%+v %v", r, err)
	}
	if len(*writes) != 0 {
		t.Errorf("writes %+v", *writes)
	}
}

func TestProfileWriteOutcomes(t *testing.T) {
	create := json.RawMessage(`{"action":"create","category":"fact","scope":"companion","body":"b","evidence":[{"kind":"user_stated","ref":"event:1"}],"reason":"said so"}`)
	entry := memory.Entry{ID: "new", Version: 1}
	for want, res := range map[string]memory.Result{
		`{"outcome":"applied","id":"new","version":1}`:                          {Outcome: memory.OutcomeApplied, Entry: entry},
		`{"outcome":"candidate","id":"new","version":1,"reason":"later"}`:       {Outcome: memory.OutcomeCandidate, Entry: entry, Reason: "later"},
		`{"outcome":"rejected","code":"bad_evidence","reason":"no such event"}`: {Outcome: memory.OutcomeRejected, Code: "bad_evidence", Reason: "no such event"},
	} {
		set, writes := profileSet(fakeProfile{result: res})
		r, err := set.Call(context.Background(), companionCaller, "profile_write", create)
		if err != nil || r.IsError || r.Text != want {
			t.Errorf("got %+v %v, want %s", r, err, want)
		}
		w := (*writes)[0]
		if w.Actor != memory.ActorCompanion || w.Session != companionCaller.ID || w.Scope != (memory.Scope{Kind: memory.ScopeCompanion, Value: "c1"}) || len(w.Evidence) != 1 {
			t.Errorf("write %+v", w)
		}
	}
}

func TestProfileWriteRetryIsOneOp(t *testing.T) {
	set, writes := profileSet(fakeProfile{result: memory.Result{Outcome: memory.OutcomeApplied}})
	ctx := context.Background()
	for _, args := range []string{
		`{"action":"create","body":"b","reason":"r"}`,
		`{"reason":"r",  "body":"b","action":"create"}`,
		`{"action":"create","body":"c","reason":"r"}`,
	} {
		if r, err := set.Call(ctx, companionCaller, "profile_write", json.RawMessage(args)); err != nil || r.IsError {
			t.Fatalf("%+v %v", r, err)
		}
	}
	w := *writes
	if w[0].OpID != w[1].OpID || w[0].OpID == w[2].OpID || !strings.HasPrefix(w[0].OpID, "tool_") || len(w[0].OpID) != 37 {
		t.Errorf("op IDs %q %q %q", w[0].OpID, w[1].OpID, w[2].OpID)
	}
}

func TestProfileWriteRefusals(t *testing.T) {
	cases := map[string]struct{ args, want string }{
		"out of scope":   {`{"action":"withdraw","id":"out","expected_version":1,"reason":"r"}`, "no entry out"},
		"not active":     {`{"action":"update","id":"cand","expected_version":1,"body":"b","reason":"r"}`, "no entry cand"},
		"no such scope":  {`{"action":"create","scope":"repo","body":"b","reason":"r"}`, "this session has no repo scope"},
		"a scope value":  {`{"action":"create","scope":"companion","scope_value":"c2","body":"b","reason":"r"}`, "bad arguments"},
		"model conflict": {`{"action":"update","id":"in","expected_version":1,"body":"b","reason":"r"}`, "version moved"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			set, _ := profileSet(fakeProfile{entries: []memory.Entry{active, outside, pending}, err: &Failure{"version moved"}})
			r, err := set.Call(context.Background(), companionCaller, "profile_write", json.RawMessage(tc.args))
			if err != nil || !r.IsError || !strings.Contains(r.Text, tc.want) {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

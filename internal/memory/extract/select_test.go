package extract

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addSession(t *testing.T, st *store.Store, sess proto.Session) {
	t.Helper()
	if sess.Mode == "" {
		sess.Mode = proto.ModePersonal
	}
	if sess.Harness.Kind == "" {
		sess.Harness.Kind = "claude"
	}
	sess.Owner, sess.Origin, sess.CreatedAt = proto.OwnerMe, proto.OriginMe, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if sess.Model == "" {
		sess.Model = "m1"
	}
	if _, err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, st *store.Store, id proto.SessionID, at time.Time, agent string, p proto.Payload) uint64 {
	t.Helper()
	res, err := st.Append(context.Background(), store.AppendRequest{Session: id, Turn: 1, Time: at, Entries: []store.Entry{{Payload: p, Agent: agent}}})
	if err != nil {
		t.Fatal(err)
	}
	return res.LastSeq
}

func said(text string) proto.Message {
	return proto.Message{ID: text[:min(8, len(text))], Role: proto.RoleUser, Text: text}
}

func TestSelectLines(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		agent string
		p     proto.Payload
		want  string
	}{
		{"user", "", proto.Message{ID: "1", Role: proto.RoleUser, Text: "hi\nthere"}, `[1] user: "hi\nthere"` + "\n"},
		{"assistant", "", proto.Message{ID: "1", Role: proto.RoleAssistant, Text: "ok"}, `[1] assistant: "ok"` + "\n"},
		{"report", "", proto.Message{ID: "1", Role: proto.RoleUser, Text: "x", From: "other"}, ""},
		{"subagent", "a1", proto.Message{ID: "1", Role: proto.RoleUser, Text: "x"}, ""},
		{"running tool", "", proto.ToolCall{ID: "t", Name: "bash", Summary: "ls", Status: proto.ToolRunning}, ""},
		{"finished tool", "", proto.ToolCall{ID: "t", Name: "bash", Summary: "ls", Status: proto.ToolDone}, `[1] tool done: "bash: ls"` + "\n"},
		{"artifact", "", proto.ArtifactPublished{Artifact: "a", Name: "plan.md", Kind: proto.ArtifactText, Version: 1, By: proto.ArtifactByCLI}, `[1] artifact: "plan.md"` + "\n"},
		{"not material", "", proto.Error{Message: "boom"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newStore(t)
			addSession(t, st, proto.Session{ID: "s"})
			put(t, st, "s", at, c.agent, c.p)
			m, err := Select(context.Background(), st, "s", 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			if m.Text != c.want || m.To != 1 {
				t.Fatalf("got %q to %d, want %q", m.Text, m.To, c.want)
			}
		})
	}
}

func TestSelectBoundary(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	overhead := len(`[1] user: ""` + "\n")
	cases := []struct {
		name   string
		extra  int
		lines  int
		to     uint64
	}{
		{"exactly full", 0, 2, 2},
		{"one byte over", 1, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newStore(t)
			addSession(t, st, proto.Session{ID: "s"})
			first := 1000
			put(t, st, "s", at, "", said(strings.Repeat("a", first)))
			room := MaxMaterial - first - overhead - overhead
			put(t, st, "s", at, "", said(strings.Repeat("b", room+c.extra)))
			put(t, st, "s", at, "", said(strings.Repeat("c", 10)))
			m, err := Select(context.Background(), st, "s", 1, 2)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(m.Text, "\n"); got != c.lines || m.To != c.to {
				t.Fatalf("lines = %d to = %d (len %d), want %d to %d", got, m.To, len(m.Text), c.lines, c.to)
			}
			if len(m.Text) > MaxMaterial {
				t.Fatalf("len = %d", len(m.Text))
			}
		})
	}
}

func TestSelectOversizeEvent(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	st := newStore(t)
	addSession(t, st, proto.Session{ID: "s"})
	put(t, st, "s", at, "", said(strings.Repeat("字", MaxMaterial)))
	put(t, st, "s", at, "", said("next"))
	m, err := Select(context.Background(), st, "s", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if m.To != 1 || len(m.Text) > MaxMaterial || !strings.HasSuffix(m.Text, truncated) || !strings.HasPrefix(m.Text, "[1] user") {
		t.Fatalf("to = %d, len = %d", m.To, len(m.Text))
	}
	if strings.ToValidUTF8(m.Text, "") != m.Text {
		t.Fatal("cut inside a character")
	}
}

func TestInputLimitsExisting(t *testing.T) {
	scopes := []memory.Scope{{Kind: memory.ScopeGlobal}, {Kind: memory.ScopeRepo, Value: "r"}}
	big := memory.Entry{ID: "a", Version: 1, Category: memory.CategoryFact, Scope: scopes[0], Body: strings.Repeat("x", maxExisting-200)}
	over := memory.Entry{ID: "b", Version: 2, Category: memory.CategoryFact, Scope: scopes[1], Body: strings.Repeat("y", 300)}
	got := input(scopes, []memory.Entry{big, over}, "[1] user: \"x\"\n")
	if !strings.HasPrefix(got, "Scopes: global repo\n") || !strings.Contains(got, `"id":"a"`) || strings.Contains(got, `"id":"b"`) || !strings.HasSuffix(got, "Session log:\n[1] user: \"x\"\n") {
		t.Fatalf("input = %.200q", got)
	}
}

func TestParse(t *testing.T) {
	ev := `"evidence":[{"kind":"user_stated","event":3}],"reason":"r"`
	create := `{"action":"create","category":"fact","scope":"global","body":"b",` + ev + `}`
	cases := []struct {
		name, in string
		ok       bool
	}{
		{"empty", `{"ops":[]}`, true},
		{"create", `{"ops":[` + create + `]}`, true},
		{"update", `{"ops":[{"action":"update","target":"t","version":2,"body":"b",` + ev + `}]}`, true},
		{"update widening", `{"ops":[{"action":"update","target":"t","version":2,"scope":"global","body":"b",` + ev + `}]}`, true},
		{"withdraw", `{"ops":[{"action":"withdraw","target":"t","version":2,` + ev + `}]}`, true},
		{"not json", `nope`, false},
		{"fence", "```json\n{\"ops\":[]}\n```", false},
		{"trailing text", `{"ops":[]} done`, false},
		{"no ops", `{}`, false},
		{"null ops", `{"ops":null}`, false},
		{"unknown field", `{"ops":[],"x":1}`, false},
		{"unknown op field", `{"ops":[{"action":"create","category":"fact","scope":"global","body":"b","x":1,` + ev + `}]}`, false},
		{"bad action", `{"ops":[{"action":"merge","target":"t","version":1,"body":"b",` + ev + `}]}`, false},
		{"bad category", `{"ops":[{"action":"create","category":"z","scope":"global","body":"b",` + ev + `}]}`, false},
		{"bad scope", `{"ops":[{"action":"create","category":"fact","scope":"z","body":"b",` + ev + `}]}`, false},
		{"no body", `{"ops":[{"action":"create","category":"fact","scope":"global",` + ev + `}]}`, false},
		{"create target", `{"ops":[{"action":"create","category":"fact","scope":"global","target":"t","body":"b",` + ev + `}]}`, false},
		{"update no version", `{"ops":[{"action":"update","target":"t","body":"b",` + ev + `}]}`, false},
		{"withdraw body", `{"ops":[{"action":"withdraw","target":"t","version":2,"body":"b",` + ev + `}]}`, false},
		{"no evidence", `{"ops":[{"action":"create","category":"fact","scope":"global","body":"b","evidence":[],"reason":"r"}]}`, false},
		{"bad kind", `{"ops":[{"action":"create","category":"fact","scope":"global","body":"b","evidence":[{"kind":"z","event":3}],"reason":"r"}]}`, false},
		{"string ref", `{"ops":[{"action":"create","category":"fact","scope":"global","body":"b","evidence":[{"kind":"observed","event":"s/3"}],"reason":"r"}]}`, false},
		{"zero event", `{"ops":[{"action":"create","category":"fact","scope":"global","body":"b","evidence":[{"kind":"observed","event":0}],"reason":"r"}]}`, false},
		{"no reason", `{"ops":[{"action":"create","category":"fact","scope":"global","body":"b","evidence":[{"kind":"observed","event":3}]}]}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.in)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok = %v", err, c.ok)
			}
		})
	}
}

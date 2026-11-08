package daemon

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// toolsOf connects as sess's tool connection.
func toolsOf(t *testing.T, d *Daemon, h string, sess proto.SessionID) *client.Client {
	t.Helper()
	token := "token-" + string(sess)
	if err := d.store.SetSessionToken(timeout(t), sess, store.TokenHash(token), time.Now()); err != nil {
		t.Fatal(err)
	}
	tc, err := dialTools(timeout(t), h, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tc.Close() })
	return tc
}

// TestDelegation checks the delegation tools end to end: a companion
// starts a child, which runs its task and reports back; the companion
// sends it more and hands work off to a Code session; a Code session can
// do none of it.
func TestDelegation(t *testing.T) {
	echo := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{echo, echo, echo}})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	comp, err := c.Create(timeout(t), proto.CreateParams{Mode: proto.ModePersonal, Harness: "fake", Permission: proto.ModeAutoEdit})
	if err != nil {
		t.Fatal(err)
	}
	ct := toolsOf(t, d, h, comp.ID)

	r := callTool(t, ct, "session_create", `{"prompt":"look it up","title":"Look up"}`)
	var child proto.Session
	if err := json.Unmarshal([]byte(r.Text), &child); r.IsError || err != nil {
		t.Fatalf("session_create %+v %v", r, err)
	}
	if child.Parent != comp.ID || child.Owner != proto.OwnerDelegated || child.Mode != proto.ModePersonal ||
		child.Permission != proto.ModeAutoEdit || child.Title != "Look up" {
		t.Fatalf("child %+v", child)
	}
	cw := watch(t, c, child.ID, 0)
	cw.until(turnEnded(1))
	if got := cw.texts(1); len(got) != 2 || got[0] != "look it up" {
		t.Fatalf("child turn 1 %q", got)
	}
	if r := callTool(t, ct, "session_send", `{"id":"`+string(child.ID)+`","text":"and also"}`); r.IsError {
		t.Fatalf("session_send %+v", r)
	}
	cw.until(turnEnded(2))

	kt := toolsOf(t, d, h, child.ID)
	if r := callTool(t, kt, "report", `{"text":"found it"}`); r.IsError || r.Text != `{"queued":true}` {
		t.Fatalf("report %+v", r)
	}
	pw := watch(t, c, comp.ID, 0)
	pw.until(turnEnded(1))
	want := `[Report from Hoplet session ` + string(child.ID) + ` "Look up"]` + "\n\nfound it"
	if got := pw.texts(1); len(got) != 2 || got[0] != "found it" || got[1] != want {
		t.Fatalf("companion turn 1 %q", got)
	}
	if rs := reportsOf(t, d.store, comp.ID); len(rs) != 1 || rs[0].From != child.ID || rs[0].Report != proto.ReportDone {
		t.Fatalf("reports %+v", rs)
	}

	// The companion cannot send to sessions it did not start.
	other, err := c.Create(timeout(t), proto.CreateParams{Mode: proto.ModePersonal, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if r := callTool(t, ct, "session_send", `{"id":"`+string(other.ID)+`","text":"x"}`); !r.IsError {
		t.Fatalf("send to another companion %+v", r)
	}

	work := filepath.Join(h, "work")
	r = callTool(t, ct, "handoff", `{"directory":"`+work+`","summary":"fix the bug"}`)
	var code proto.Session
	if err := json.Unmarshal([]byte(r.Text), &code); r.IsError || err != nil {
		t.Fatalf("handoff %+v %v", r, err)
	}
	if code.Mode != proto.ModeCode || code.Owner != proto.OwnerMe || code.Parent != comp.ID ||
		code.Permission != proto.ModeConfirm || code.Directory != work {
		t.Fatalf("handed off %+v", code)
	}
	watch(t, c, code.ID, 0).until(turnEnded(1))
	var got []proto.SessionDelegated
	evs, err := d.store.Events(timeout(t), comp.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type == proto.TypeSessionDelegated {
			p, _ := ev.Decode()
			got = append(got, p.(proto.SessionDelegated))
		}
	}
	if want := []proto.SessionDelegated{{Session: child.ID, Kind: proto.DelegatedSession, Title: "Look up"},
		{Session: code.ID, Kind: proto.HandedOff}}; !slices.Equal(got, want) {
		t.Fatalf("delegations %+v, want %+v", got, want)
	}
	if r := callTool(t, ct, "session_send", `{"id":"`+string(code.ID)+`","text":"x"}`); !r.IsError {
		t.Fatalf("send to the handed-off session %+v", r)
	}
	if r := callTool(t, ct, "session_create", `{"prompt":"x","harness":"nope"}`); !r.IsError || !strings.Contains(r.Text, "unknown harness") {
		t.Fatalf("unknown harness %+v", r)
	}

	// A Code session sees none of the delegation tools.
	codeTools := toolsOf(t, d, h, create(t, c, h).ID)
	for _, name := range []string{"session_create", "session_send", "handoff", "report"} {
		if r := callTool(t, codeTools, name, `{}`); !r.IsError || !strings.Contains(r.Text, "no tool named") {
			t.Errorf("%s from a code session %+v", name, r)
		}
	}
	// The child of an archived parent cannot report.
	if err := c.Archive(timeout(t), comp.ID); err != nil {
		t.Fatal(err)
	}
	if r := callTool(t, kt, "report", `{"text":"late"}`); !r.IsError || !strings.Contains(r.Text, "archived") {
		t.Fatalf("report to an archived parent %+v", r)
	}
}

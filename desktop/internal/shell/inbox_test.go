package shell

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

var errGone = errors.New("connection lost")

func TestInboxSections(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	archived := t0
	run := proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run"}
	edit := proto.ApprovalRequested{ID: "a2", Kind: proto.ApprovalFileChange, Title: "Edit"}
	here := machineFeed{name: "macbook", connected: true,
		sessions: []proto.Session{
			{ID: "w1", Status: proto.StatusWaiting, UpdatedAt: at(1)},
			{ID: "w2", Status: proto.StatusWaiting, UpdatedAt: at(5)},
			{ID: "u1", Status: proto.StatusIdle, Unread: true, UpdatedAt: at(2)},
			{ID: "e1", Status: proto.StatusErrored, Unread: true, UpdatedAt: at(3)},
			{ID: "x1", Status: proto.StatusWaiting, ArchivedAt: &archived},
			{ID: "i1", Status: proto.StatusIdle},
		},
		pending: map[proto.SessionID][]proto.ApprovalRequested{"w1": {run, edit}},
	}
	away := machineFeed{name: "vps", offlineSince: t0, sessions: []proto.Session{
		{ID: "v1", Status: proto.StatusWaiting}, {ID: "v2", Status: proto.StatusIdle, Unread: true},
	}}
	for name, tc := range map[string]struct {
		feeds []machineFeed
		want  []string // label: kind/session/approval of each card
	}{
		"nothing": {feeds: []machineFeed{{name: "macbook", connected: true}}},
		"one machine": {feeds: []machineFeed{here}, want: []string{
			"需要你: w2/-", "需要你: w1/a1", "需要你: w1/a2",
			"已完成，待查看: u1", "出错: e1",
		}},
		"a machine away": {feeds: []machineFeed{here, away}, want: []string{
			"需要你: w2/-", "需要你: w1/a1", "需要你: w1/a2",
			"已完成，待查看: u1", "出错: e1", "断线: vps [v1]",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			var got []string
			for _, sec := range inboxSections(tc.feeds) {
				for _, it := range sec.items {
					switch {
					case it.kind == inboxOffline:
						var ids []string
						for _, s := range it.waiting {
							ids = append(ids, string(s.ID))
						}
						got = append(got, sec.label+": "+it.machine+" "+"["+join(ids)+"]")
					case it.kind == inboxApproval && it.approval == nil:
						got = append(got, sec.label+": "+string(it.session.ID)+"/-")
					case it.kind == inboxApproval:
						got = append(got, sec.label+": "+string(it.session.ID)+"/"+it.approval.ID)
					default:
						got = append(got, sec.label+": "+string(it.session.ID))
					}
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

// inboxApp is the test app with s2 waiting on a command to run.
func inboxApp(t *testing.T) (*App, *host, *ui.Tester) {
	t.Helper()
	a, h, tt, _ := newApp(t)
	h.states = map[proto.SessionID]proto.SessionState{"s2": {Pending: []proto.ApprovalRequested{{
		ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test", Detail: "go test ./...",
		Options: []proto.ApprovalOption{
			{ID: "once", Label: "允许一次", Scope: proto.ScopeOnce},
			{ID: "sess", Label: "本会话总是允许 go test", Scope: proto.ScopeSession},
			{ID: "no", Label: "拒绝", Scope: proto.ScopeDeny},
		},
	}}}}
	a.OpenInbox()
	tt.Frame()
	tt.Frame()
	return a, h, tt
}

func lastRespond(t *testing.T, h *host) proto.RespondParams {
	t.Helper()
	for i := len(h.calls) - 1; i >= 0; i-- {
		if r, ok := h.calls[i].(proto.RespondParams); ok {
			return r
		}
	}
	t.Fatal("no answer sent")
	return proto.RespondParams{}
}

// An approval of another session is answered on its card, on its machine.
func TestInboxApprove(t *testing.T) {
	a, h, tt := inboxApp(t)
	if !slices.Contains(h.asked, "s2") || slices.Contains(h.asked, "s1") {
		t.Fatalf("asked for %v", h.asked)
	}
	for _, want := range []string{"需要你 · 1", "Add tray", "go test ./...", "允许一次", "本会话总是允许 go test", "拒绝", "studio · /w/hoplet"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("允许一次"); err != nil {
		t.Fatal(err)
	}
	if r := lastRespond(t, h); r.Session != "s2" || r.Approval != "a1" || r.Option != "once" {
		t.Fatalf("answered %+v", r)
	}
	tt.Frame()
	if !tt.HasText("已允许一次 · ") || tt.HasText("go test ./...") {
		t.Fatalf("no result line in %q", tt.Texts())
	}
	// The session moves on: the approval and its line go.
	h.states["s2"] = proto.SessionState{}
	s2, _ := a.cat.Get("s2")
	s2.Status, s2.Version = proto.StatusIdle, s2.Version+1
	a.Apply(proto.CatalogItem{Version: a.cat.Version() + 1, Sessions: []proto.Session{s2}})
	tt.Frame()
	if tt.HasText("已允许一次 · ") || tt.HasText("需要你 · 1") {
		t.Fatalf("the handled approval stays: %q", tt.Texts())
	}
}

// Number keys answer the selected card, and the selection moves on.
func TestInboxKeys(t *testing.T) {
	a, h, tt := inboxApp(t)
	tt.Key(0, ui.Key3)
	if r := lastRespond(t, h); r.Option != "no" {
		t.Fatalf("3 answered %+v", r)
	}
	if a.s.inboxSel != 1 {
		t.Fatalf("selection at %d", a.s.inboxSel)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter) // the next card is s1's, unread
	tt.Frame()
	if a.selected != "s1" || a.inbox {
		t.Fatalf("↩ opened %q (inbox %v)", a.selected, a.inbox)
	}
}

// Unread sessions are marked read from the inbox, one by one or all.
func TestInboxMarkRead(t *testing.T) {
	_, h, tt := inboxApp(t)
	if !tt.HasText("已完成，待查看 · 1") {
		t.Fatalf("no unread group in %q", tt.Texts())
	}
	if err := tt.Click("全部标为已读"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.updates); n == 0 || h.updates[n-1].Session != "s1" || h.updates[n-1].Unread == nil || *h.updates[n-1].Unread {
		t.Fatalf("updates %+v", h.updates)
	}
}

// A machine out of reach is one card; what waited there cannot be
// answered until it is back.
func TestInboxOffline(t *testing.T) {
	a, h, tt := inboxApp(t)
	a.Lost(errGone)
	tt.Frame()
	for _, want := range []string{"断线 · 1", "studio", "立即重连", "重连后会话自动续上，期间的审批保留。"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("允许一次") {
		t.Fatal("an approval answerable while its machine is away")
	}
	if err := tt.Click("立即重连"); err != nil {
		t.Fatal(err)
	}
	if h.reconnects == 0 {
		t.Fatal("no reconnect")
	}
}

func TestInboxEmpty(t *testing.T) {
	a, _, tt, _ := newApp(t)
	a.Apply(proto.CatalogItem{Snapshot: true, Version: a.cat.Version() + 1})
	a.OpenInbox()
	tt.Frame()
	if !tt.HasText("没有要你处理的事") {
		t.Fatalf("no empty state in %q", tt.Texts())
	}
}

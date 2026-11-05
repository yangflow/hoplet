package convo

import (
	"reflect"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

var t0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// log builds events with seqs from first, one second apart.
func log(t *testing.T, first uint64, turn uint32, ps ...proto.Payload) []proto.Event {
	t.Helper()
	var out []proto.Event
	for i, p := range ps {
		seq := first + uint64(i)
		ev, err := proto.NewEvent("s", seq, turn, t0.Add(time.Duration(seq)*time.Second), p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func kinds(items []Item) []Kind {
	var out []Kind
	for _, it := range items {
		out = append(out, it.Kind)
	}
	return out
}

func TestTurn(t *testing.T) {
	a := proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test", Options: []proto.ApprovalOption{{ID: "ok", Label: "Allow", Scope: proto.ScopeOnce}}}
	var c Convo
	c.Apply(proto.SessionItem{Events: log(t, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "fix it"}, // 1
		proto.TurnStarted{},                        // 2
		proto.Thinking{Text: "look at the bridge"}, // 3
		proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolRunning}, // 4
		proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolDone},    // 5
		proto.MessageDelta{MessageID: "m1", Text: "Found "},                                                           // 6
		proto.MessageDelta{MessageID: "m1", Text: "it"},                                                               // 7
	)})
	if !c.Running() {
		t.Fatal("not running")
	}
	items := c.Items()
	if want := []Kind{KindTurn, KindMine, KindWork, KindText}; !reflect.DeepEqual(kinds(items), want) {
		t.Fatalf("kinds %v, want %v", kinds(items), want)
	}
	if w := items[2].Work; len(w.Entries) != 2 || w.Entries[1].Duration() != time.Second || w.Running || w.Failed != 0 {
		t.Fatalf("work %+v", w)
	}
	if it := items[3]; it.Text != "Found it" || !it.Streaming {
		t.Fatalf("streaming text %+v", it)
	}

	c.Apply(proto.SessionItem{Events: log(t, 8, 1,
		proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "Found it."},                                       // 8
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "go test", Status: proto.ToolRunning}, // 9
		a, // 10
		proto.Message{ID: "steer-1", Role: proto.RoleUser, Text: "also lint"}, // 11
	)})
	if got := c.Pending(); len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("pending %+v", got)
	}
	items = c.Items()
	if want := []Kind{KindTurn, KindMine, KindWork, KindText, KindWork, KindApproval, KindMine}; !reflect.DeepEqual(kinds(items), want) {
		t.Fatalf("kinds %v, want %v", kinds(items), want)
	}
	if items[3].Text != "Found it." || items[3].Streaming || !items[6].Steer || items[1].Steer {
		t.Fatalf("items %+v", items)
	}
	if w := items[4].Work; !w.Running {
		t.Fatal("the running group is not marked running")
	}

	c.Apply(proto.SessionItem{Events: log(t, 12, 1,
		proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "ok", DecidedBy: "device:mac"},   // 12
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "go test", Status: proto.ToolFailed}, // 13
		proto.FileChange{Path: "bridge.go", Op: proto.FileModified},                                                // 14
		proto.FileChange{Path: "bridge.go", Op: proto.FileModified},                                                // 15
		proto.Error{Message: "rate limited"},                                                                       // 16
		proto.TurnEnded{Reason: proto.TurnInterrupted},                                                             // 17
	)})
	items = c.Items()
	if want := []Kind{KindTurn, KindMine, KindWork, KindText, KindWork, KindApproval, KindMine, KindError, KindStopped, KindChanges}; !reflect.DeepEqual(kinds(items), want) {
		t.Fatalf("kinds %v, want %v", kinds(items), want)
	}
	if c.Running() || len(c.Pending()) != 0 {
		t.Fatalf("after the turn: running %v, pending %+v", c.Running(), c.Pending())
	}
	if items[0].Duration != 15*time.Second {
		t.Fatalf("turn took %v", items[0].Duration)
	}
	if w := items[4].Work; w.Running || w.Failed != 1 {
		t.Fatalf("finished group %+v", w)
	}
	if ap := items[5].Approval; ap.Outcome != proto.OutcomeAllowed || ap.DecidedBy != "device:mac" || ap.Request.Title != "Run go test" {
		t.Fatalf("approval record %+v", ap)
	}
	if f := items[9].Files; len(f) != 1 || f[0].Path != "bridge.go" {
		t.Fatalf("change card %+v", f)
	}
	if c.LastSeq() != 17 {
		t.Fatalf("last seq %d", c.LastSeq())
	}
}

func TestSnapshotPagesAndQuestion(t *testing.T) {
	var c Convo
	c.Apply(proto.SessionItem{Snapshot: &proto.SessionSnapshot{More: true, Events: log(t, 10, 3,
		proto.TurnStarted{}, proto.Question{ID: "q1", Text: "Which one?", Options: []string{"a", "b"}},
	)}, Queue: &proto.Queue{Messages: []proto.QueuedMessage{{Key: "q", Text: "next"}}}})
	if q, ok := c.Question(); !ok || q.ID != "q1" {
		t.Fatalf("question %+v %v", q, ok)
	}
	if more, first := c.More(); !more || first != 10 {
		t.Fatalf("more %v first %d", more, first)
	}
	if len(c.Queue()) != 1 {
		t.Fatalf("queue %+v", c.Queue())
	}
	// Events already held are skipped.
	c.Apply(proto.SessionItem{Events: log(t, 11, 3, proto.Question{ID: "q1", Text: "Which one?"})})
	if len(c.Items()) != 1 {
		t.Fatalf("items %+v", c.Items())
	}
	c.Prepend(proto.EventsPage{Events: log(t, 8, 2, proto.TurnStarted{}, proto.TurnEnded{Reason: proto.TurnCompleted})})
	if more, first := c.More(); more || first != 8 || len(c.Items()) != 2 {
		t.Fatalf("after paging back: more %v first %d items %+v", more, first, c.Items())
	}
	c.Apply(proto.SessionItem{Events: log(t, 12, 3, proto.TurnEnded{Reason: proto.TurnCompleted})})
	if _, ok := c.Question(); ok {
		t.Fatal("the question outlived its turn")
	}
	// A new snapshot replaces everything.
	c.Apply(proto.SessionItem{Snapshot: &proto.SessionSnapshot{}})
	if len(c.Items()) != 0 || c.LastSeq() != 0 {
		t.Fatalf("after an empty snapshot: %+v", c.Items())
	}
}

// The context's use is the last report; the window's size stays once said.
func TestContextUsage(t *testing.T) {
	var c Convo
	if _, ok := c.Context(); ok {
		t.Fatal("usage before any report")
	}
	c.Apply(proto.SessionItem{Events: log(t, 1, 1,
		proto.ContextUsage{Used: 20000, Window: 200000},
		proto.ContextUsage{Used: 30000},
	)})
	if u, ok := c.Context(); !ok || u != (proto.ContextUsage{Used: 30000, Window: 200000}) {
		t.Fatalf("usage %+v %v", u, ok)
	}
}

// A message I sent shows, with the turn starting, until the daemon
// records it; then it shows once.
func TestEcho(t *testing.T) {
	var c Convo
	c.Echo("k1", "hello", t0)
	items := c.Items()
	if !c.Starting() || len(items) != 1 || items[0].Kind != KindMine || items[0].Text != "hello" {
		t.Fatalf("echo: starting %v, items %+v", c.Starting(), items)
	}
	c.Apply(proto.SessionItem{Events: log(t, 1, 1, proto.Message{ID: "k1", Role: proto.RoleUser, Text: "hello"}, proto.TurnStarted{})})
	mine := 0
	for _, it := range c.Items() {
		if it.Kind == KindMine {
			mine++
			if it.Key == "echo:k1" {
				t.Fatal("the echo stays after the message is recorded")
			}
		}
	}
	if c.Starting() || mine != 1 {
		t.Fatalf("recorded: starting %v, %d of mine", c.Starting(), mine)
	}
	c.Echo("k2", "again", t0)
	c.Unecho()
	if c.Starting() {
		t.Fatal("the echo stays after it failed")
	}
}

// A turn's steps, its approval and the text between them are one group;
// only the text after the last step stays apart.
func TestGrouped(t *testing.T) {
	a := proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test"}
	var c Convo
	c.Apply(proto.SessionItem{Events: log(t, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "fix it"}, // 1
		proto.TurnStarted{},                        // 2
		proto.Message{ID: "m0", Text: "Looking."},  // 3
		proto.Thinking{Text: "look at the bridge"}, // 4
		proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolDone}, // 5
		proto.Message{ID: "m1", Text: "The timeout leaks."},                                                        // 6
		a, // 7
		proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "ok"},                            // 8
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "go test", Status: proto.ToolFailed}, // 9
		proto.Message{ID: "m2", Text: "Fixed."},                                                                    // 10
	)})
	items := c.Grouped()
	if want := []Kind{KindTurn, KindMine, KindWork, KindText}; !reflect.DeepEqual(kinds(items), want) {
		t.Fatalf("kinds %v, want %v", kinds(items), want)
	}
	w := items[2].Work
	var got []string
	for _, e := range w.Entries {
		switch {
		case e.Tool != nil:
			got = append(got, "tool "+e.Tool.ID)
		case e.Approval != nil:
			got = append(got, "approval "+string(e.Approval.Outcome))
		case e.Note != "":
			got = append(got, "note "+e.Note)
		default:
			got = append(got, "think "+e.Thinking)
		}
	}
	want := []string{"note Looking.", "think look at the bridge", "tool t1", "note The timeout leaks.", "approval allowed", "tool t2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries %q\nwant %q", got, want)
	}
	if items[2].Key != "work:4" || w.Failed != 1 || w.Running || !w.Start.Equal(t0.Add(3*time.Second)) {
		t.Fatalf("group %q %+v", items[2].Key, w)
	}
	if items[3].Text != "Fixed." {
		t.Fatalf("reply %+v", items[3])
	}
	// Before the reply, the group runs.
	var r Convo
	r.Apply(proto.SessionItem{Events: log(t, 1, 1, proto.TurnStarted{}, proto.Message{ID: "m0", Text: "Looking."}, proto.Thinking{Text: "hm"})})
	if g := r.Grouped(); len(g) != 2 || !g[1].Work.Running || len(g[1].Work.Entries) != 2 {
		t.Fatalf("running group %+v", g)
	}
}

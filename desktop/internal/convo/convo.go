// Package convo turns a session's event log into what the conversation
// shows: turn separators, my messages, the harness's text, work groups of
// thinking and tool calls, approval records, errors, stops and change
// cards; and keeps what waits on me: open approvals, a question, the
// queue.
package convo

import (
	"slices"
	"strconv"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// Kind is the kind of an item.
type Kind int

// Item kinds.
const (
	KindTurn     Kind = iota // a separator at a turn's start
	KindMine                 // a message of mine; Steer when sent into a running turn
	KindText                 // the harness's text
	KindWork                 // thinking and tool calls between two texts
	KindApproval             // a decided approval
	KindError                // something went wrong
	KindStopped              // the turn was stopped
	KindChanges              // the files a turn changed
	KindEvent                // a delegation, a handoff or a publication
	KindReport               // a report from another session
)

// Item is one entry of the conversation.
type Item struct {
	Kind Kind
	Key  string    // stable across rebuilds
	Time time.Time // when it started

	Turn     uint32
	Duration time.Duration // a turn's length; 0 while it runs

	Text      string
	Steer     bool
	Streaming bool
	Unsent    bool // a message of mine that did not go out

	Work     *Work
	Approval *Approval
	Files    []proto.FileChange
	Event    *Event
	Report   *Report
}

// Event is something the session made elsewhere. Delegation is set for a
// session it delegated or handed off, Artifact for a version it published.
type Event struct {
	Delegation proto.DelegationKind
	Session    proto.SessionID
	Artifact   proto.ArtifactID
	Kind       proto.ArtifactKind
	Version    int
	Title      string // as recorded; a session's current title may differ
}

// Report is another session's report; its body is the item's Text.
type Report struct {
	From   proto.SessionID
	Status proto.ReportStatus
}

// Work is a group of thinking and tool calls.
type Work struct {
	Entries []Entry
	Running bool // the turn is still running and this is its last group
	Failed  int
	Start   time.Time
	End     time.Time
}

// Entry is one step of a work group: Tool is set for a tool call, Agent
// for a subagent, Thinking for visible reasoning; in a turn's group, Note
// for text the harness wrote between steps and Approval for an approval.
type Entry struct {
	Key      string // stable across rebuilds
	Tool     *proto.ToolCall
	Agent    *proto.Subagent
	Thinking string
	Note     string
	Approval *Approval
	Start    time.Time
	End      time.Time // zero while the call runs
}

// Duration is how long the step took; 0 while it runs.
func (e Entry) Duration() time.Duration {
	if e.End.IsZero() {
		return 0
	}
	return e.End.Sub(e.Start)
}

// Subagent is a subagent sent out, as last reported, and when it was sent
// and ended; End is zero while it runs.
type Subagent struct {
	proto.Subagent
	Start time.Time
	End   time.Time
}

// Took is how long it ran: as the harness reported, or else from when it
// was sent until it ended or now.
func (g Subagent) Took(now time.Time) time.Duration {
	switch {
	case g.DurationMS > 0:
		return time.Duration(g.DurationMS) * time.Millisecond
	case g.End.IsZero():
		return now.Sub(g.Start)
	}
	return g.End.Sub(g.Start)
}

// Approval is a decided approval.
type Approval struct {
	Request   proto.ApprovalRequested
	Outcome   proto.ApprovalOutcome
	OptionID  string
	DecidedBy string
	At        time.Time
}

// Convo is one session's conversation. The zero value is empty.
type Convo struct {
	// agent, when set, makes this the record of that subagent: only its
	// events count. Otherwise its events are left out.
	agent   string
	session proto.Session
	events  []proto.Event
	more    bool // older events exist before the first one held
	queue   []proto.QueuedMessage

	built   bool
	items   []Item
	grouped []Item
	pending []proto.ApprovalRequested
	asked   *proto.Question
	running bool
	context proto.ContextUsage
	todos   []proto.TodoItem
	agents  []Subagent
	live    bool // a record whose subagent still runs

	// echo is a message I just sent that the daemon has not recorded yet:
	// it shows, with the turn starting, until a message of its ID comes.
	echo     *proto.Message
	echoAt   time.Time
	echoErr  string // why the echo did not go out
	starting bool
}

// Record returns an empty record of a subagent's own events.
func Record(agent string) *Convo { return &Convo{agent: agent} }

// Echo shows a message I just sent, by its idempotency key, until the
// daemon records it: a new session starts its harness first, which takes
// a moment.
func (c *Convo) Echo(key, text string, at time.Time) {
	c.echo, c.echoAt, c.echoErr, c.built = &proto.Message{ID: key, Role: proto.RoleUser, Text: text}, at, "", false
}

// Unecho drops the message shown as sent.
func (c *Convo) Unecho() {
	if c.echo != nil {
		c.echo, c.built = nil, false
	}
}

// Unsent marks the message shown as sent, by its key, as not gone out,
// and why; an empty why marks it going out again.
func (c *Convo) Unsent(key, why string) {
	if c.echo != nil && c.echo.ID == key {
		c.echoErr, c.built = why, false
	}
}

// Failed returns the message that did not go out, its key and why; false
// when none waits.
func (c *Convo) Failed() (text, key, why string, ok bool) {
	c.build()
	if c.echo == nil || c.echoErr == "" {
		return "", "", "", false
	}
	return c.echo.Text, c.echo.ID, c.echoErr, true
}

// Starting reports whether a message I sent waits for the daemon, the
// turn about to start.
func (c *Convo) Starting() bool {
	c.build()
	return c.starting
}

// Apply brings the conversation up to a subscription item: a snapshot
// replaces what is held, events append.
func (c *Convo) Apply(it proto.SessionItem) {
	if s := it.Snapshot; s != nil {
		c.session, c.events, c.more = s.Session, slices.Clone(s.Events), s.More
	}
	for _, ev := range it.Events {
		if n := len(c.events); n > 0 && ev.Seq <= c.events[n-1].Seq {
			continue // already held
		}
		c.events = append(c.events, ev)
	}
	if it.Queue != nil {
		c.queue = slices.Clone(it.Queue.Messages)
	}
	c.built = false
}

// Prepend adds older events, as paging back returns them.
func (c *Convo) Prepend(page proto.EventsPage) {
	if len(page.Events) == 0 {
		c.more = false
		return
	}
	first := uint64(0)
	if len(c.events) > 0 {
		first = c.events[0].Seq
	}
	var older []proto.Event
	for _, ev := range page.Events {
		if first == 0 || ev.Seq < first {
			older = append(older, ev)
		}
	}
	c.events, c.more = append(older, c.events...), page.More
	c.built = false
}

// More reports whether older events remain to page back to; First is the
// oldest seq held.
func (c *Convo) More() (bool, uint64) {
	if len(c.events) == 0 {
		return c.more, 0
	}
	return c.more, c.events[0].Seq
}

// LastSeq is the seq to resume the subscription after.
func (c *Convo) LastSeq() uint64 {
	if len(c.events) == 0 {
		return 0
	}
	return c.events[len(c.events)-1].Seq
}

// Items returns the conversation's items, oldest first.
func (c *Convo) Items() []Item {
	c.build()
	return c.items
}

// Grouped returns the items with each stretch of a turn before its reply
// in one work group: thinking, tool calls, approvals and the text between
// them become its entries, and only the text after the last of them stays
// apart, as the reply. Grouped is what a Code conversation shows, where a
// publication is no row and a report reads as a message.
func (c *Convo) Grouped() []Item {
	c.build()
	return c.grouped
}

// Pending returns the open approvals, oldest first.
func (c *Convo) Pending() []proto.ApprovalRequested {
	c.build()
	return c.pending
}

// Question returns the question the harness asked in the running turn, if
// it waits.
func (c *Convo) Question() (proto.Question, bool) {
	c.build()
	if c.asked == nil {
		return proto.Question{}, false
	}
	return *c.asked, true
}

// Running reports whether a turn runs.
func (c *Convo) Running() bool {
	c.build()
	return c.running
}

// Context reports how full the context window is, as the harness last
// said; false before it said anything.
func (c *Convo) Context() (proto.ContextUsage, bool) {
	c.build()
	return c.context, c.context.Used > 0
}

// Changed returns the seq of the last event that can change the session's
// files, a turn ending or a file changing; 0 with none held.
func (c *Convo) Changed() uint64 {
	for i := len(c.events) - 1; i >= 0; i-- {
		switch c.events[i].Type {
		case proto.TypeTurnEnded, proto.TypeFileChange:
			return c.events[i].Seq
		}
	}
	return 0
}

// Todos returns the task list the harness last reported; nil before it
// reported one.
func (c *Convo) Todos() []proto.TodoItem {
	c.build()
	return c.todos
}

// Agents returns the subagents sent out, each as last reported, in the
// order they were sent.
func (c *Convo) Agents() []Subagent {
	c.build()
	return c.agents
}

// Agent returns a subagent as last reported.
func (c *Convo) Agent(id string) (Subagent, bool) {
	for _, g := range c.Agents() {
		if g.ID == id {
			return g, true
		}
	}
	return Subagent{}, false
}

// Live marks a record's subagent as still running, which keeps its last
// work group open.
func (c *Convo) Live(on bool) {
	if c.live != on {
		c.live, c.built = on, false
	}
}

// Task returns what a record's subagent was asked to do: its first
// message, which is mine.
func (c *Convo) Task() string {
	for _, it := range c.Items() {
		if it.Kind == KindMine {
			return it.Text
		}
	}
	return ""
}

// Queue returns the messages waiting for the turn to end.
func (c *Convo) Queue() []proto.QueuedMessage { return c.queue }

// builder holds the state of one pass over the events.
type builder struct {
	items    []Item
	turnAt   int // index of the running turn's separator, -1 for none
	sawReply bool
	texts    map[string]int // message ID → item index
	tools    map[string]int // tool call ID → index of its work group
	agentAt  map[string]int // subagent ID → index of its work group
	agents   []Subagent
	todos    []proto.TodoItem
	approval map[string]int // approval ID → item index
	pending  map[string]proto.ApprovalRequested
	order    []string // pending approval IDs in request order
	files    []proto.FileChange
	asked    *proto.Question
	context  proto.ContextUsage
	echoed   bool // the echo's message is recorded
	echo     string
}

func (c *Convo) build() {
	if c.built {
		return
	}
	b := &builder{turnAt: -1, texts: map[string]int{}, tools: map[string]int{}, agentAt: map[string]int{}, approval: map[string]int{}, pending: map[string]proto.ApprovalRequested{}}
	if c.echo != nil {
		b.echo = c.echo.ID
	}
	for _, ev := range c.events {
		if ev.Agent != c.agent {
			continue // another record's
		}
		p, err := ev.Decode()
		if err != nil {
			continue // a type this client does not know yet
		}
		b.event(ev, p)
	}
	if b.turnAt >= 0 {
		// The running turn's last work group still works unless text
		// followed it; approvals, steers and errors do not close it.
	scan:
		for i := len(b.items) - 1; i > b.turnAt; i-- {
			switch b.items[i].Kind {
			case KindWork:
				b.items[i].Work.Running = true
				break scan
			case KindText:
				break scan
			}
		}
	}
	if c.echo != nil && b.echoed {
		c.echo = nil
	}
	c.starting = c.echo != nil && c.echoErr == ""
	if c.echo != nil {
		b.items = append(b.items, Item{Kind: KindMine, Key: "echo:" + c.echo.ID, Time: c.echoAt, Text: c.echo.Text, Unsent: c.echoErr != ""})
	}
	c.items, c.running, c.asked, c.context = b.items, b.turnAt >= 0 || c.live, b.asked, b.context
	c.todos, c.agents = b.todos, b.agents
	c.grouped = group(b.items, c.running)
	c.pending = c.pending[:0]
	for _, id := range b.order {
		if a, ok := b.pending[id]; ok {
			c.pending = append(c.pending, a)
		}
	}
	c.built = true
}

func (b *builder) add(it Item) int {
	b.items = append(b.items, it)
	return len(b.items) - 1
}

// lastWork returns the work group the next step joins: the last item, when
// it is a work group.
func (b *builder) lastWork() *Work {
	if n := len(b.items); n > 0 && b.items[n-1].Kind == KindWork {
		return b.items[n-1].Work
	}
	return nil
}

func (b *builder) work(ev proto.Event) *Work {
	if w := b.lastWork(); w != nil {
		return w
	}
	w := &Work{Start: ev.Time}
	b.add(Item{Kind: KindWork, Key: key("work", ev.Seq), Time: ev.Time, Turn: ev.Turn, Work: w})
	return w
}

func (b *builder) event(ev proto.Event, p proto.Payload) {
	switch p := p.(type) {
	case proto.TurnStarted:
		turn := Item{Kind: KindTurn, Key: key("turn", ev.Seq), Time: ev.Time, Turn: ev.Turn}
		if n := len(b.items); n > 0 && b.items[n-1].Kind == KindMine {
			// The separator heads the turn, above the message that
			// started it. Nothing holds the index of that message yet.
			b.items = slices.Insert(b.items, n-1, turn)
			b.turnAt = n - 1
		} else {
			b.turnAt = b.add(turn)
		}
		b.sawReply, b.files, b.asked = false, nil, nil
	case proto.TurnEnded:
		if b.turnAt >= 0 {
			b.items[b.turnAt].Duration = ev.Time.Sub(b.items[b.turnAt].Time)
		}
		switch p.Reason {
		case proto.TurnInterrupted:
			b.add(Item{Kind: KindStopped, Key: key("stop", ev.Seq), Time: ev.Time, Turn: ev.Turn})
		case proto.TurnAborted, proto.TurnFailed:
			if p.Message != "" {
				b.add(Item{Kind: KindError, Key: key("err", ev.Seq), Time: ev.Time, Turn: ev.Turn, Text: p.Message})
			}
		}
		if len(b.files) > 0 {
			b.add(Item{Kind: KindChanges, Key: key("files", ev.Seq), Time: ev.Time, Turn: ev.Turn, Files: b.files})
		}
		b.turnAt, b.files, b.asked = -1, nil, nil
		// The turn's open approvals expire with it (the store resolves
		// them in the same append).
	case proto.SessionDelegated:
		b.add(Item{Kind: KindEvent, Key: key("event", ev.Seq), Time: ev.Time, Turn: ev.Turn, Event: &Event{Delegation: p.Kind, Session: p.Session, Title: p.Title}})
	case proto.ArtifactPublished:
		b.add(Item{Kind: KindEvent, Key: key("event", ev.Seq), Time: ev.Time, Turn: ev.Turn, Event: &Event{Artifact: p.Artifact, Kind: p.Kind, Version: p.Version, Title: p.Name}})
	case proto.Message:
		if p.Role == proto.RoleUser && p.From != "" {
			status := p.Report
			if status == "" {
				status = proto.ReportDone // recorded before reports had a status
			}
			b.add(Item{Kind: KindReport, Key: key("report", ev.Seq), Time: ev.Time, Turn: ev.Turn, Text: p.Text, Report: &Report{From: p.From, Status: status}})
			return
		}
		if p.Role == proto.RoleUser {
			b.echoed = b.echoed || b.echo != "" && p.ID == b.echo
			b.add(Item{Kind: KindMine, Key: key("mine", ev.Seq), Time: ev.Time, Turn: ev.Turn, Text: p.Text, Steer: b.sawReply && b.turnAt >= 0})
			return
		}
		b.sawReply = true
		if i, ok := b.texts[p.ID]; ok {
			b.items[i].Text, b.items[i].Streaming = p.Text, false
			return
		}
		b.texts[p.ID] = b.add(Item{Kind: KindText, Key: "text:" + p.ID, Time: ev.Time, Turn: ev.Turn, Text: p.Text})
	case proto.MessageDelta:
		b.sawReply = true
		if i, ok := b.texts[p.MessageID]; ok {
			b.items[i].Text += p.Text
			return
		}
		b.texts[p.MessageID] = b.add(Item{Kind: KindText, Key: "text:" + p.MessageID, Time: ev.Time, Turn: ev.Turn, Text: p.Text, Streaming: true})
	case proto.Thinking:
		b.sawReply = true
		w := b.work(ev)
		w.Entries = append(w.Entries, Entry{Key: key("think", ev.Seq), Thinking: p.Text, Start: ev.Time, End: ev.Time})
		w.End = ev.Time
	case proto.ToolCall:
		b.sawReply = true
		tc := p
		if i, ok := b.tools[p.ID]; ok {
			// A later state of a call updates its row where it is.
			w := b.items[i].Work
			for j := range w.Entries {
				if e := &w.Entries[j]; e.Tool != nil && e.Tool.ID == p.ID {
					if p.Status == proto.ToolFailed && e.Tool.Status != proto.ToolFailed {
						w.Failed++
					}
					e.Tool = &tc
					if p.Status != proto.ToolRunning {
						e.End, w.End = ev.Time, ev.Time
					}
				}
			}
			return
		}
		w := b.work(ev)
		b.tools[p.ID] = len(b.items) - 1
		e := Entry{Key: "tool:" + p.ID, Tool: &tc, Start: ev.Time}
		if p.Status != proto.ToolRunning {
			e.End = ev.Time
		}
		if p.Status == proto.ToolFailed {
			w.Failed++
		}
		w.Entries = append(w.Entries, e)
		w.End = ev.Time
	case proto.Subagent:
		b.subagent(ev, p)
	case proto.TodoList:
		b.todos = p.Items
	case proto.FileChange:
		for _, f := range b.files {
			if f.Path == p.Path {
				return
			}
		}
		b.files = append(b.files, p)
	case proto.ApprovalRequested:
		b.pending[p.ID] = p
		b.order = append(b.order, p.ID)
		b.approval[p.ID] = b.add(Item{Kind: KindApproval, Key: "approval:" + p.ID, Time: ev.Time, Turn: ev.Turn, Approval: &Approval{Request: p}})
	case proto.ApprovalResolved:
		delete(b.pending, p.ID)
		if i, ok := b.approval[p.ID]; ok {
			a := b.items[i].Approval
			a.Outcome, a.OptionID, a.DecidedBy, a.At = p.Outcome, p.OptionID, p.DecidedBy, ev.Time
		}
	case proto.Question:
		q := p
		b.asked = &q
	case proto.Error:
		b.add(Item{Kind: KindError, Key: key("err", ev.Seq), Time: ev.Time, Turn: ev.Turn, Text: p.Message})
	case proto.ContextUsage:
		if p.Window == 0 {
			p.Window = b.context.Window // the window's size stays once said
		}
		b.context = p
	}
}

// subagent adds a subagent sent out to the work group it was sent in, or
// updates it where it is.
func (b *builder) subagent(ev proto.Event, p proto.Subagent) {
	g := p
	b.sawReply = true
	if i, ok := b.agentAt[p.ID]; ok {
		for j := range b.agents {
			if g := &b.agents[j]; g.ID == p.ID {
				g.Subagent = p
				if p.Status != proto.SubagentRunning && g.End.IsZero() {
					g.End = ev.Time
				}
			}
		}
		w := b.items[i].Work
		for j := range w.Entries {
			if e := &w.Entries[j]; e.Agent != nil && e.Agent.ID == p.ID {
				e.Agent = &g
				if p.Status != proto.SubagentRunning && e.End.IsZero() {
					e.End = ev.Time
					if !p.Background {
						// One in the background may end long after
						// its group: the group keeps its own time.
						w.End = ev.Time
					}
				}
			}
		}
		return
	}
	run := Subagent{Subagent: p, Start: ev.Time}
	if p.Status != proto.SubagentRunning {
		run.End = ev.Time
	}
	b.agents = append(b.agents, run)
	w := b.work(ev)
	b.agentAt[p.ID] = len(b.items) - 1
	e := Entry{Key: "agent:" + p.ID, Agent: &g, Start: ev.Time}
	if p.Status != proto.SubagentRunning {
		e.End = ev.Time
	}
	w.Entries = append(w.Entries, e)
	w.End = ev.Time
}

func key(kind string, seq uint64) string {
	return kind + ":" + strconv.FormatUint(seq, 10)
}

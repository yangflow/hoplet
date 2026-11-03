package shell

import (
	"crypto/rand"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/proto"
)

// sessionView is the state of the content area beyond the sidebar: each
// session's conversation and draft, what is folded or loaded, the
// composer's popovers and the new-session page.
type sessionView struct {
	convos    map[proto.SessionID]*convo.Convo
	followed  proto.SessionID
	drafts    map[proto.SessionID]string
	outbox    []proto.SendParams // messages written while offline, in order
	harnesses []proto.HarnessInfo
	missing   []proto.HarnessInfo // harnesses Hoplet knows but cannot run, and why

	open      map[string]bool              // work groups, tool rows and code blocks I opened or closed
	outputs   map[string]*proto.ToolOutput // loaded tool outputs, by session and call
	outputErr map[string]string            // why a tool output could not be read
	scroll    ui.ScrollState
	md        markdown.Options

	permOpen, runOpen, detailOpen, dirOpen bool
	pick                                   int                  // the highlighted option of the approval shown
	hidden                                 map[string]bool      // approvals I put away with ⎋
	copied                                 map[string]time.Time // when each copy button last copied
	armed                                  time.Time            // the first ⎋ of two that stop a turn

	diffs        map[proto.SessionID]*diffState                // each session's changes, as last read
	queue        ui.ListState                                  // the queue tray's rows, which drag to reorder
	queueAll     bool                                          // the queue shows every message, not its first three
	offlineSince time.Time                                     // when the connection was lost
	flash        map[proto.SessionID]bool                      // rows of sessions made elsewhere, and whether they lit
	inboxPending map[proto.SessionID][]proto.ApprovalRequested // approvals of sessions waiting on me, as last read
	inboxAsked   map[proto.SessionID]uint64                    // the session version each was read at
	inboxDone    map[string]string                             // what I decided, by approval, until it goes
	inboxSel     int                                           // the card the keys chose, -1 before they choose one
	inboxFocus   bool                                          // the inbox takes the focus this frame
	palOpen      bool                                          // the command palette shows
	palQuery     string                                        // what the palette searches for
	palScope     paletteScope                                  // what it searches: all, sessions or commands
	palSel       int                                           // the selected row
	runSub       string                                        // the run menu's open submenu: subEffort, subModel or none
	runHover     string                                        // the run menu row the pointer is on
	modelTab     string                                        // the harness whose models show
	modelQuery   string                                        // what the model search holds
	modelFocus   bool                                          // the model search takes the focus this frame
	modelPick    int                                           // the highlighted model, -1 for the current

	newPage bool
	newPick newChoice
}

// newChoice is what the new-session page has chosen; harness is run's.
type newChoice struct {
	run  runSettings
	dir  string
	name string // a companion's name
}

func newSessionView() sessionView {
	return sessionView{
		convos: map[proto.SessionID]*convo.Convo{}, drafts: map[proto.SessionID]string{},
		open: map[string]bool{}, outputs: map[string]*proto.ToolOutput{}, outputErr: map[string]string{}, hidden: map[string]bool{}, copied: map[string]time.Time{},
		diffs: map[proto.SessionID]*diffState{}, flash: map[proto.SessionID]bool{},
		inboxPending: map[proto.SessionID][]proto.ApprovalRequested{}, inboxAsked: map[proto.SessionID]uint64{}, inboxDone: map[string]string{},
	}
}

// conv returns the conversation of a session, made empty on first use.
func (a *App) conv(id proto.SessionID) *convo.Convo {
	c, ok := a.s.convos[id]
	if !ok {
		c = &convo.Convo{}
		a.s.convos[id] = c
	}
	return c
}

// follow follows the selected session from what is already held.
func (a *App) follow() {
	s, ok := a.current()
	if !ok || !a.connected {
		return
	}
	a.s.followed = s.ID
	a.host.Follow(s.ID, a.conv(s.ID).LastSeq())
}

// ApplySession brings a session's conversation up to a subscription item.
func (a *App) ApplySession(id proto.SessionID, it proto.SessionItem) {
	a.conv(id).Apply(it)
}

// SetHarnesses records the installed harnesses and their models, and
// apart from them those that cannot run.
func (a *App) SetHarnesses(hs []proto.HarnessInfo) {
	a.s.harnesses, a.s.missing = nil, nil
	for _, h := range hs {
		if h.Missing != "" {
			a.s.missing = append(a.s.missing, h)
		} else {
			a.s.harnesses = append(a.s.harnesses, h)
		}
	}
}

// harness returns the description of a harness kind.
func (a *App) harness(kind string) (proto.HarnessInfo, bool) {
	i := slices.IndexFunc(a.s.harnesses, func(h proto.HarnessInfo) bool { return h.Kind == kind })
	if i < 0 {
		return proto.HarnessInfo{}, false
	}
	return a.s.harnesses[i], true
}

// NewSession opens the new-session page in the current mode.
func (a *App) NewSession() { a.newSessionIn("") }

// newSessionIn opens the new-session page with a directory already chosen,
// and records it for Back and Forward.
func (a *App) newSessionIn(dir string) {
	a.showNew(dir)
	a.visit(page{kind: pageNew, mode: a.mode})
}

// showNew shows the new-session page of the current mode, without
// recording it.
func (a *App) showNew(dir string) {
	a.s.newPage, a.selected, a.inbox = true, "", false
	a.s.newPick = newChoice{dir: dir}
	if a.mode == proto.ModeCode {
		a.s.newPick.run.perm = proto.ModeAutoReview // the daemon's default for Code
	}
	if a.connected {
		// Offline, the list comes when the connection does.
		a.host.Harnesses(a.SetHarnesses)
	}
}

// send sends the draft of the selected session: into the queue while a
// turn runs, into the outbox while offline.
func (a *App) send(s proto.Session, text string) {
	p := proto.SendParams{Session: s.ID, Text: text, Key: "desktop-" + rand.Text()}
	if !a.connected {
		a.s.outbox = append(a.s.outbox, p)
		return
	}
	if !a.conv(s.ID).Running() {
		// Until the daemon records it (a new session first starts its
		// harness), it shows as sent.
		a.conv(s.ID).Echo(p.Key, text, time.Now())
	}
	a.host.Send(p)
}

// flushOutbox sends what was written while offline, in order.
func (a *App) flushOutbox() {
	for _, p := range a.s.outbox {
		a.host.Send(p)
	}
	a.s.outbox = nil
}

// outbox returns the messages waiting to go out for a session.
func (a *App) outbox(id proto.SessionID) []string {
	var out []string
	for _, p := range a.s.outbox {
		if p.Session == id {
			out = append(out, p.Text)
		}
	}
	return out
}

// diffState is a session's changes as last read, and the event seq they
// were asked for at.
type diffState struct {
	diff  *proto.SessionDiff
	sent  bool   // a question went out
	asked uint64 // the conversation's last seq when it did
}

// diff returns a session's changes, asking again when its conversation
// moved on since the last ask; nil until the first answer.
func (a *App) diff(s proto.Session) *proto.SessionDiff {
	st, ok := a.s.diffs[s.ID]
	if !ok {
		st = &diffState{}
		a.s.diffs[s.ID] = st
	}
	if seq := a.conv(s.ID).LastSeq(); a.connected && s.Directory != "" && (!st.sent || seq > st.asked) && !a.conv(s.ID).Running() {
		st.sent, st.asked = true, seq
		a.host.Diff(s.ID, func(d proto.SessionDiff) { st.diff = &d })
	}
	return st.diff
}

// fileStats finds a changed file's line counts in a session's diff; path
// is as the harness reported it, absolute or relative to the directory.
func fileStats(d *proto.SessionDiff, dir, path string) (proto.FileDiff, bool) {
	if d == nil {
		return proto.FileDiff{}, false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	for _, r := range d.Repos {
		root := filepath.Join(dir, r.Path)
		for _, f := range r.Files {
			if filepath.Join(root, f.Path) == path {
				return f, true
			}
		}
	}
	return proto.FileDiff{}, false
}

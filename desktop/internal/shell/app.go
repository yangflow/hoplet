// Package shell is the desktop window: the sidebar of sessions and the
// content area's title bar. It keeps the view state; the daemon and the
// operating system are reached through Host.
package shell

import (
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/icons"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// Host carries out what the shell asks. Calls must not block: daemon
// requests run elsewhere and their effects come back through the catalog
// or the followed session, their failures through App.Fail; a done
// function runs on the UI thread.
type Host interface {
	Update(p proto.UpdateParams)
	Stop(id proto.SessionID)
	Archive(id proto.SessionID)
	// Reveal shows a directory in Finder.
	Reveal(dir string)

	Create(p proto.CreateParams, done func(proto.Session))
	// Send sends a message; done follows on the UI thread with nil once
	// the daemon took it, or why it did not.
	Send(p proto.SendParams, done func(error))
	Steer(p proto.SteerParams)
	Interrupt(id proto.SessionID)
	Respond(p proto.RespondParams)
	Answer(p proto.AnswerParams)
	SetPermission(p proto.SetPermissionParams)
	SetPlan(p proto.SetPlanParams)
	SetEffort(p proto.SetEffortParams)
	SetFast(p proto.SetFastParams)
	SetUltracode(p proto.SetUltracodeParams)
	SetModel(p proto.SetModelParams)
	EditQueued(p proto.QueueEditParams)
	DeleteQueued(p proto.QueueKeyParams)
	MoveQueued(p proto.QueueMoveParams)
	Output(p proto.OutputParams, done func(proto.ToolOutput, error))
	// Diff reads a session's changes since it began, without patches.
	Diff(id proto.SessionID, done func(proto.SessionDiff))
	// DiffPatch reads them with their patches; done follows with the
	// answer or why there is none.
	DiffPatch(id proto.SessionID, done func(proto.SessionDiff, error))
	// Reconnect tries the daemon now rather than at the next retry.
	Reconnect()
	// Older pages back through a session's events before seq.
	Older(id proto.SessionID, before uint64, done func(proto.EventsPage))
	Harnesses(done func([]proto.HarnessInfo))
	// Session reads what a session waits on: its pending approvals.
	Session(id proto.SessionID, done func(proto.SessionState))
	// Follow follows one session's events from after seq, in place of the
	// session followed before; its items reach App.ApplySession.
	Follow(id proto.SessionID, after uint64)
	// ChooseFolder asks for a directory; done is not called when I cancel.
	ChooseFolder(done func(dir string))
}

// State is what the window offers outside itself: the menu bar, the menu
// bar icon and the Dock follow it.
type State struct {
	Connected bool
	Counts    catalog.Counts
	// NeedsYou and Running list the live sessions in those states.
	NeedsYou, Running []Entry
	// Selected is the selected session; its ID is empty when there is none.
	Selected Selection
	// CanBack and CanForward say whether Back and Forward have somewhere
	// to go.
	CanBack, CanForward bool
}

// Entry names a session in a menu.
type Entry struct {
	ID   proto.SessionID
	Name string
}

// Selection is what the session menu needs to know of the selected
// session.
type Selection struct {
	ID       proto.SessionID
	Pinned   bool
	Running  bool
	Archived bool
	HasDir   bool
}

func (s State) equal(o State) bool {
	return s.Connected == o.Connected && s.Counts == o.Counts && s.Selected == o.Selected &&
		s.CanBack == o.CanBack && s.CanForward == o.CanForward &&
		slices.Equal(s.NeedsYou, o.NeedsYou) && slices.Equal(s.Running, o.Running)
}

// App is the window's state. Use it from the UI thread only.
type App struct {
	icons *icons.Set
	host  Host

	cat       catalog.Catalog
	connected bool
	machine   string // the daemon's host name
	offline   string // why there is no connection

	mode      proto.Mode
	sort      catalog.Sort
	show      catalog.Show
	archived  bool
	selected  string
	closed    map[string]bool // collapsed directory groups
	hideSide  bool
	sideMoves bool    // the sidebar is opening or closing
	side      float32 // the sidebar's width, which its edge drags
	away      bool    // the app is in the background or the window hidden

	panelOpen   bool         // I opened the right panel
	panelMoves  bool         // the right panel is changing width
	panelTarget float32      // the width the right panel moves to
	panelFrom   float32      // the width it moves from
	panelMotion ui.Animation // how it moves there
	width       float32      // the window's width in the last frame
	viewerWas   bool         // the right panel last rested with its viewer
	panelW      float32      // the right panel's width, which its edge drags
	sideAuto    bool         // the right panel hid the sidebar to make room

	inbox bool // the inbox page shows

	// history is the pages I opened, oldest first; at is the current one,
	// -1 before the first.
	history []page
	at      int

	renaming proto.SessionID
	draft    string

	pending ui.Toast // a note for the view to show

	s sessionView // the conversation, the composer and the new-session page

	changed func(State)
	last    State
}

// New returns the app in Code mode, not yet connected, on the new-session
// page, as it is whenever no session is selected. changed is called from
// the view whenever State changes.
func New(set *icons.Set, host Host, changed func(State)) *App {
	a := &App{icons: set, host: host, mode: proto.ModeCode, closed: map[string]bool{}, at: -1, changed: changed, s: newSessionView(), side: theme.SidebarWidth, panelW: theme.PanelWidth}
	a.newSessionIn("")
	return a
}

// Connected records a new connection to the daemon of host: the followed
// session resumes and messages written while offline go out.
func (a *App) Connected(host string) {
	a.connected, a.machine, a.offline, a.s.offlineSince = true, host, "", time.Time{}
	if a.s.newPage {
		a.host.Harnesses(a.SetHarnesses)
	}
	a.follow()
	a.flushOutbox()
}

// Lost records that the connection is gone and why.
func (a *App) Lost(err error) {
	if a.connected || a.s.offlineSince.IsZero() {
		a.s.offlineSince = time.Now()
	}
	a.connected = false
	a.offline = err.Error()
}

// Fail shows that something asked of the host did not happen, until it is
// closed or another note replaces it; retry, when not nil, asks again.
func (a *App) Fail(err error, retry func()) {
	a.pending = failure("没做成："+err.Error(), retry)
}

// Apply brings the session list up to a catalog item. A session that
// appears in an update, made elsewhere, lights its row for a moment.
func (a *App) Apply(it proto.CatalogItem) {
	if !it.Snapshot && a.cat.Version() > 0 {
		for _, s := range it.Sessions {
			if _, ok := a.cat.Get(s.ID); !ok {
				a.s.flash[s.ID] = false // lights when first drawn
			}
		}
	}
	a.cat.Apply(it)
	a.markRead()
}

// SetMode switches between Personal and Code.
func (a *App) SetMode(m proto.Mode) {
	if a.mode != m {
		a.mode, a.archived = m, false
		a.newSessionIn("")
	}
}

// ToggleSidebar hides or shows the sidebar.
func (a *App) ToggleSidebar() {
	a.hideSide, a.sideMoves, a.sideAuto = !a.hideSide, true, false
	if !a.hideSide {
		a.sideShown()
	}
}

// maxHistory bounds how far Back goes.
const maxHistory = 100

// pageKind is what a page of the window shows.
type pageKind int

// Pages.
const (
	pageSession pageKind = iota
	pageInbox
	pageNew
)

// page is a place Back and Forward go to: a session, the inbox, or the
// new-session page of a mode.
type page struct {
	kind pageKind
	id   proto.SessionID
	mode proto.Mode
}

// visit adds p to the history after the current page, unless it is the
// current page.
func (a *App) visit(p page) {
	if a.at >= 0 && a.history[a.at] == p {
		return
	}
	a.history = append(a.history[:a.at+1], p)
	if len(a.history) > maxHistory {
		a.history = a.history[len(a.history)-maxHistory:]
	}
	a.at = len(a.history) - 1
}

// Select selects a session, switching to its mode, and records it for
// Back and Forward.
func (a *App) Select(id proto.SessionID) {
	if a.open(id) {
		a.visit(page{kind: pageSession, id: id})
	}
}

// OpenInbox shows the inbox and records it for Back and Forward.
func (a *App) OpenInbox() {
	a.showInbox()
	a.visit(page{kind: pageInbox})
}

func (a *App) showInbox() {
	a.inbox, a.selected, a.s.newPage = true, "", false
	a.s.inboxSel, a.s.inboxFocus = -1, true
}

// open selects a session without recording it; it reports whether the
// session exists.
func (a *App) open(id proto.SessionID) bool {
	s, ok := a.cat.Get(id)
	if !ok {
		return false
	}
	a.mode, a.archived, a.selected = s.Mode, s.ArchivedAt != nil, string(id)
	a.s.newPage, a.inbox = false, false
	a.markRead()
	a.follow()
	return true
}

// Back goes to the previous page in the history that still exists.
func (a *App) Back() { a.travel(-1) }

// Forward goes to the next page in the history that still exists.
func (a *App) Forward() { a.travel(1) }

func (a *App) travel(dir int) {
	i := a.step(dir)
	if i < 0 {
		return
	}
	a.at = i
	switch p := a.history[i]; p.kind {
	case pageSession:
		a.open(p.id)
	case pageInbox:
		a.showInbox()
	case pageNew:
		a.mode = p.mode
		a.showNew("")
	}
}

// step returns the index of the nearest page in direction dir (-1 or 1)
// from the current one that still exists, or -1: a session must still be
// in the catalog.
func (a *App) step(dir int) int {
	for i := a.at + dir; i >= 0 && i < len(a.history); i += dir {
		p := a.history[i]
		if p.kind != pageSession {
			return i
		}
		if _, ok := a.cat.Get(p.id); ok {
			return i
		}
	}
	return -1
}

// SetAway records whether I can see the window: not while the app is in
// the background or the window is hidden. Coming back marks the selected
// session read.
func (a *App) SetAway(away bool) {
	a.away = away
	a.markRead()
}

// markRead clears the unread mark of the selected session while I can see
// it.
func (a *App) markRead() {
	if s, ok := a.current(); ok && s.Unread && !a.away {
		off := false
		a.host.Update(proto.UpdateParams{Session: s.ID, Unread: &off})
	}
}

func (a *App) current() (proto.Session, bool) {
	return a.cat.Get(proto.SessionID(a.selected))
}

// TogglePin pins or unpins the selected session.
func (a *App) TogglePin() {
	if s, ok := a.current(); ok {
		on := !s.Pinned
		a.host.Update(proto.UpdateParams{Session: s.ID, Pinned: &on})
	}
}

// MarkUnread marks the selected session unread.
func (a *App) MarkUnread() {
	if s, ok := a.current(); ok {
		on := true
		a.host.Update(proto.UpdateParams{Session: s.ID, Unread: &on})
	}
}

// Rename starts renaming the selected session in its row.
func (a *App) Rename() {
	if s, ok := a.current(); ok {
		a.renaming, a.draft = s.ID, s.Title
	}
}

// Reveal shows the selected session's directory in Finder.
func (a *App) Reveal() {
	if s, ok := a.current(); ok && s.Directory != "" {
		a.host.Reveal(s.Directory)
	}
}

// Stop stops the selected session's harness.
func (a *App) Stop() {
	if s, ok := a.current(); ok && s.Status == proto.StatusRunning {
		a.host.Stop(s.ID)
	}
}

// Archive archives the selected session.
func (a *App) Archive() {
	if s, ok := a.current(); ok && s.ArchivedAt == nil {
		a.host.Archive(s.ID)
		a.newSessionIn("")
	}
}

func (a *App) commitRename() {
	if s, ok := a.cat.Get(a.renaming); ok && a.draft != s.Title {
		title := a.draft
		a.host.Update(proto.UpdateParams{Session: s.ID, Title: &title})
	}
	a.renaming = ""
}

// state computes State.
func (a *App) state() State {
	st := State{Connected: a.connected, Counts: a.cat.Counts(), CanBack: a.step(-1) >= 0, CanForward: a.step(1) >= 0}
	if s, ok := a.current(); ok {
		st.Selected = Selection{ID: s.ID, Pinned: s.Pinned, Running: s.Status == proto.StatusRunning,
			Archived: s.ArchivedAt != nil, HasDir: s.Directory != ""}
	}
	for _, m := range []proto.Mode{proto.ModeCode, proto.ModePersonal} {
		for _, s := range all(a.cat.List(catalog.View{Mode: m, Show: catalog.ShowNeedsYou})) {
			st.NeedsYou = append(st.NeedsYou, Entry{s.ID, name(s)})
		}
		for _, s := range all(a.cat.List(catalog.View{Mode: m, Show: catalog.ShowRunning})) {
			st.Running = append(st.Running, Entry{s.ID, name(s)})
		}
	}
	return st
}

// all returns a list's sessions in order, pinned ones first.
func all(l catalog.List) []proto.Session {
	out := slices.Clone(l.Pinned)
	for _, g := range l.Groups {
		out = append(out, g.Sessions...)
	}
	return out
}

// View draws the window.
func (a *App) View(c *ui.Context) {
	k := theme.Apply(c)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	a.side = min(max(a.side, sideMin(c.TitleBar())), theme.SidebarMax)
	width, _ := c.Size()
	a.width = width
	// The sidebar opens and closes by its width, along motion-smooth; at
	// rest and open it sits in a split its edge drags.
	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	target := a.side
	if a.hideSide {
		target = 0
	}
	// Whole DIPs: a fractional width puts the centered conversation on a
	// different subpixel each frame, and its text shimmers as it moves.
	shown := float32(math.Round(float64(root.AnimateTo("side", target, theme.MotionBase))))
	if a.sideMoves && (root.Arrived("side") || math.Abs(float64(shown-target)) < 0.5) {
		a.sideMoves = false
		c.AnimationFrame() // settle into the split, or let the sidebar go
	}
	root.Children(func() {
		switch {
		case !a.hideSide && !a.sideMoves:
			ui.Split(c, &a.side, func() { a.sidebar(c, k) }, func() { a.main(c, k, width-a.side) }).Fill()
		default:
			if a.sideMoves || !a.hideSide {
				ui.Column(c).Width(shown).ClipX().Shrink(0).BorderWidth(0, theme.LineWidth, 0, 0).BorderColor(k.Line).Children(func() {
					ui.Column(c).Width(a.side).Grow(1).Children(func() { a.sidebar(c, k) })
				})
			}
			ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() { a.main(c, k, width-shown) })
		}
	})
	a.toasts(c, k)
	if c.Shortcut(ui.Cmd, ui.KeyK) {
		a.OpenPalette()
	}
	a.palette(c, k)
	if st := a.state(); !st.equal(a.last) {
		a.last = st
		if a.changed != nil {
			a.changed(st)
		}
	}
}

package shell

import (
	"crypto/rand"
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

	open    map[string]bool              // work groups, tool rows and code blocks I opened or closed
	outputs map[string]*proto.ToolOutput // loaded tool outputs, by session and call
	scroll  ui.ScrollState
	md      markdown.Options

	permOpen, effortOpen, detailOpen bool
	pick                             int                  // the highlighted option of the approval shown
	hidden                           map[string]bool      // approvals I put away with ⎋
	copied                           map[string]time.Time // when each copy button last copied
	armed                            time.Time            // the first ⎋ of two that stop a turn

	newPage bool
	newPick newChoice
}

// newChoice is what the new-session page has chosen.
type newChoice struct {
	harness string
	dir     string
	name    string // a companion's name
}

func newSessionView() sessionView {
	return sessionView{
		convos: map[proto.SessionID]*convo.Convo{}, drafts: map[proto.SessionID]string{},
		open: map[string]bool{}, outputs: map[string]*proto.ToolOutput{}, hidden: map[string]bool{}, copied: map[string]time.Time{},
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

// SetHarnesses records the installed harnesses and their models.
func (a *App) SetHarnesses(hs []proto.HarnessInfo) { a.s.harnesses = hs }

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

// newSessionIn opens the new-session page with a directory already chosen.
func (a *App) newSessionIn(dir string) {
	a.s.newPage, a.selected = true, ""
	a.s.newPick = newChoice{dir: dir}
	a.host.Harnesses(a.SetHarnesses)
}

// send sends the draft of the selected session: into the queue while a
// turn runs, into the outbox while offline.
func (a *App) send(s proto.Session, text string) {
	p := proto.SendParams{Session: s.ID, Text: text, Key: "desktop-" + rand.Text()}
	if !a.connected {
		a.s.outbox = append(a.s.outbox, p)
		return
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

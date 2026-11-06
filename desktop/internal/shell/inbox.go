package shell

import (
	"cmp"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// machineFeed is what the inbox knows of one machine: whether it is
// reached, since when not, its sessions and the approvals they wait on.
// The inbox gathers the machines; M1 has only this one.
type machineFeed struct {
	name         string
	connected    bool
	offlineSince time.Time
	sessions     []proto.Session
	pending      map[proto.SessionID][]proto.ApprovalRequested
}

// inboxKind is what an inbox card is about.
type inboxKind int

// Inbox cards.
const (
	inboxApproval inboxKind = iota // a decision a session waits on
	inboxUnread                    // a turn that ended unseen
	inboxErrored                   // a session that failed
	inboxOffline                   // a machine out of reach
)

// inboxItem is a card of the inbox. It keeps its machine, which its
// actions go to.
type inboxItem struct {
	machine  string
	kind     inboxKind
	session  proto.Session
	approval *proto.ApprovalRequested // inboxApproval; nil until it is read
	since    time.Time                // inboxOffline: when the machine went
	waiting  []proto.Session          // inboxOffline: what waited on me then
}

// inboxSection is a group of the inbox.
type inboxSection struct {
	label string
	kind  inboxKind
	items []inboxItem
}

// inboxSections groups what waits on me across machines: approvals, turns
// ended unseen, sessions that failed and machines out of reach, the newest
// first in each; empty groups are left out. A machine out of reach is one
// card, with what waited on me when it went.
func inboxSections(feeds []machineFeed) []inboxSection {
	secs := []inboxSection{
		{label: "需要你", kind: inboxApproval},
		{label: "已完成，待查看", kind: inboxUnread},
		{label: "出错", kind: inboxErrored},
		{label: "断线", kind: inboxOffline},
	}
	for _, f := range feeds {
		if !f.connected {
			it := inboxItem{machine: f.name, kind: inboxOffline, since: f.offlineSince}
			for _, s := range f.sessions {
				if s.ArchivedAt == nil && s.Status == proto.StatusWaiting {
					it.waiting = append(it.waiting, s)
				}
			}
			secs[3].items = append(secs[3].items, it)
			continue
		}
		for _, s := range f.sessions {
			if s.ArchivedAt != nil {
				continue
			}
			switch {
			case s.Status == proto.StatusWaiting:
				aps := f.pending[s.ID]
				if len(aps) == 0 { // not read yet
					secs[0].items = append(secs[0].items, inboxItem{machine: f.name, kind: inboxApproval, session: s})
				}
				for i := range aps {
					secs[0].items = append(secs[0].items, inboxItem{machine: f.name, kind: inboxApproval, session: s, approval: &aps[i]})
				}
			case s.Status == proto.StatusErrored:
				secs[2].items = append(secs[2].items, inboxItem{machine: f.name, kind: inboxErrored, session: s})
			case s.Unread:
				secs[1].items = append(secs[1].items, inboxItem{machine: f.name, kind: inboxUnread, session: s})
			}
		}
	}
	out := secs[:0]
	for _, sec := range secs {
		if len(sec.items) == 0 {
			continue
		}
		slices.SortStableFunc(sec.items, func(a, b inboxItem) int {
			return cmp.Compare(b.session.UpdatedAt.UnixNano(), a.session.UpdatedAt.UnixNano())
		})
		out = append(out, sec)
	}
	return out
}

// inboxFeeds are the machines the inbox shows, and asks each connected
// one for the approvals of its sessions that wait on me whose version
// changed since it last asked.
func (a *App) inboxFeeds() []machineFeed {
	var sessions []proto.Session
	for _, m := range []proto.Mode{proto.ModeCode, proto.ModePersonal} {
		sessions = append(sessions, all(a.cat.List(catalog.View{Mode: m}))...)
	}
	f := machineFeed{name: a.machine, connected: a.connected, offlineSince: a.s.offlineSince, sessions: sessions, pending: a.s.inboxPending}
	if f.name == "" {
		f.name = "本机"
	}
	if a.connected {
		for _, s := range sessions {
			if s.Status != proto.StatusWaiting {
				delete(a.s.inboxPending, s.ID)
				continue
			}
			if v, ok := a.s.inboxAsked[s.ID]; ok && v == s.Version {
				continue
			}
			a.s.inboxAsked[s.ID] = s.Version
			a.host.Session(s.ID, func(st proto.SessionState) {
				a.s.inboxPending[s.ID] = st.Pending
				a.pruneDone()
			})
		}
	}
	return []machineFeed{f}
}

// inboxTitle is the inbox's title bar: its icon, its name and how many
// cards each group has.
func (a *App) inboxTitle(c *ui.Context, k theme.Colors) {
	ui.Icon(c, a.icons.Inbox).FontSize(theme.Icon).TextColor(k.Fg3)
	theme.UIStrong.On(ui.Text(c, "收件箱")).TextColor(k.Fg)
	var parts []string
	for _, sec := range inboxSections(a.inboxFeeds()) {
		parts = append(parts, fmt.Sprintf("%s %d", shortLabel(sec.kind), len(sec.items)))
	}
	if len(parts) > 0 {
		theme.Caption.On(ui.Text(c, strings.Join(parts, " · "))).TextColor(k.Muted)
	}
}

func shortLabel(k inboxKind) string {
	switch k {
	case inboxApproval:
		return "需要你"
	case inboxUnread:
		return "待查看"
	case inboxErrored:
		return "出错"
	}
	return "断线"
}

// inboxMotion is how a handled card goes: up a little and out, its room
// closing, the cards under it moving up.
var inboxMotion = ui.ElementTransition{Animation: &theme.MotionBase, Exit: &ui.Motion{Y: -theme.MotionLift, Collapse: true}}

// inboxPage is the inbox: what waits on me, grouped, handled on the cards.
// ↑↓ move among the cards, ↩ opens the selected one's session, 1–3 choose
// an approval's options.
func (a *App) inboxPage(c *ui.Context, k theme.Colors) {
	secs := inboxSections(a.inboxFeeds())
	if len(secs) == 0 {
		ui.Column(c).Grow(1).Center().Gap(theme.Space2).Children(func() {
			ui.Icon(c, a.icons.Inbox).FontSize(theme.IconEmpty).TextColor(k.Faint)
			theme.UI.On(ui.Text(c, "没有要你处理的事")).TextColor(k.Muted)
		})
		return
	}
	var flat []inboxItem
	for _, sec := range secs {
		flat = append(flat, sec.items...)
	}
	// No card shows selected until the keys choose one; until then they
	// act on the first.
	a.s.inboxSel = min(a.s.inboxSel, len(flat)-1)
	sel := flat[max(a.s.inboxSel, 0)]
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, theme.Space6).Children(func() {
			col := ui.Column(c).Grow(1).Shrink(1).MaxWidth(theme.CodeColumn).Padding(theme.Space6, 0, theme.Space8, 0).Gap(theme.Space3).
				Focusable().FocusRing(false).Role(ui.RoleList).Label("收件箱")
			if a.s.inboxFocus {
				col.Focus()
				a.s.inboxFocus = false
			}
			switch {
			case col.Shortcut(0, ui.KeyUp):
				a.s.inboxSel = max(a.s.inboxSel-1, 0)
			case col.Shortcut(0, ui.KeyDown):
				a.s.inboxSel = min(a.s.inboxSel+1, len(flat)-1)
			case col.Shortcut(0, ui.KeyEnter):
				a.openFromInbox(sel)
			}
			for i, key := range []ui.Key{ui.Key1, ui.Key2, ui.Key3} {
				if col.Shortcut(0, key) {
					if opts := inboxOptions(sel.approval); i < len(opts) {
						a.inboxDecide(sel, opts[i])
					}
				}
			}
			col.Children(func() {
				n := 0
				for _, sec := range secs {
					a.inboxHeader(c, k, sec)
					for _, it := range sec.items {
						a.inboxCard(c, k, it, n)
						n++
					}
				}
			})
		})
	})
}

// inboxHeader is a group's title with its count; the unread group can be
// marked read at once.
func (a *App) inboxHeader(c *ui.Context, k theme.Colors, sec inboxSection) {
	ui.Row(c.Key("sec:"+sec.label)).Padding(theme.Space3, theme.Space1, theme.Space1).AlignItems(ui.Center).Children(func() {
		theme.Label.On(ui.Textf(c, "%s · %d", sec.label, len(sec.items))).TextColor(k.Muted).Grow(1)
		if sec.kind != inboxUnread {
			return
		}
		b := ui.Button(c, "").Label("全部标为已读").Padding(0, theme.Space1).Background(ui.Transparent).Border(0, ui.Transparent)
		if b.Clicked() {
			for _, it := range sec.items {
				a.markReadAt(it)
			}
		}
		b.Children(func() {
			theme.Caption.On(ui.Text(c, "全部标为已读")).FontWeight(theme.UIMedium.Weight()).TextColor(k.Fg3)
		})
	})
}

// inboxCard draws one card; the n-th of the inbox is selected when n is
// inboxSel.
func (a *App) inboxCard(c *ui.Context, k theme.Colors, it inboxItem, n int) {
	key := fmt.Sprintf("%s/%d/%s", it.machine, it.kind, it.session.ID)
	if it.approval != nil {
		key += "/" + it.approval.ID
	}
	if it.approval != nil && a.s.inboxDone[it.approval.ID] != "" {
		// Handled: one line of what was decided, until the session moves
		// on and the card goes.
		ui.Row(c.Key(key)).Padding(theme.Space2, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line2).BorderStyle(ui.BorderDashed).
			Transition(inboxMotion).Children(func() {
			ui.Icon(c, a.icons.Check).FontSize(theme.IconRow).TextColor(k.Add)
			theme.UI.On(ui.Text(c, a.s.inboxDone[it.approval.ID]+" · ")).TextColor(k.Fg3)
			theme.UI.On(ui.Text(c, name(it.session))).TextColor(k.Fg2).SingleLine().Shrink(1)
		})
		return
	}
	on := n == a.s.inboxSel
	card := ui.Column(c.Key(key)).Padding(theme.Space3).Gap(theme.Space2).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Background(k.Surface).
		Transition(inboxMotion)
	if on {
		card.Background(k.Selected).Border(theme.LineWidth, k.Line2)
	}
	hovered := card.Hovered()
	if card.Clicked() {
		a.s.inboxSel = n
		if it.kind == inboxUnread || it.kind == inboxErrored {
			a.openFromInbox(it)
		}
	}
	card.Children(func() {
		if it.kind == inboxOffline {
			a.offlineCard(c, k, it)
			return
		}
		ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
			if it.kind == inboxUnread {
				ui.Box(c).Size(theme.DotSmall, theme.DotSmall).Radius(theme.RadiusPill).Background(k.AccentFg)
			}
			a.harnessIcon(c, k, it.session.Harness.Kind, theme.IconRow)
			theme.UIStrong.On(ui.Text(c, name(it.session))).TextColor(k.Fg).SingleLine().Shrink(1)
			theme.Caption.On(ui.Text(c, it.machine+" · "+where(it.session))).TextColor(k.Muted).SingleLine().Shrink(1)
			ui.Spacer(c)
			if it.kind == inboxUnread && hovered {
				b := ui.Button(c, "").Label("标为已读").Height(theme.RowDense).Padding(0, theme.Space2).Radius(theme.RadiusControl).
					Border(0, ui.Transparent).Background(k.Fill)
				if b.Clicked() {
					a.markReadAt(it)
				}
				b.Children(func() { theme.Secondary.On(ui.Text(c, "标为已读")).TextColor(k.Fg) })
			}
			theme.Caption.On(ui.Text(c, ago(c.Now(), it.session.UpdatedAt))).TextColor(k.Muted)
			if iconButton(c, k, a.icons.Open, "打开会话 ↩", false).Size(theme.RowDense, theme.RowDense).Clicked() {
				a.openFromInbox(it)
			}
		})
		ui.Column(c).Padding(0, 0, 0, theme.IconRow+theme.Space2).Gap(theme.Space2).Children(func() { a.inboxBody(c, k, it) })
	})
}

// inboxBody is a card's content below its head.
func (a *App) inboxBody(c *ui.Context, k theme.Colors, it inboxItem) {
	switch it.kind {
	case inboxUnread:
		if it.session.Preview != "" {
			theme.UI.On(ui.Text(c, it.session.Preview)).TextColor(k.Fg2).MaxLines(2)
		}
	case inboxErrored:
		ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, a.icons.Failed).FontSize(theme.IconRow).TextColor(k.Del)
			text := "出错了"
			if it.session.Preview != "" {
				text += " · " + it.session.Preview
			}
			theme.UI.On(ui.Text(c, text)).TextColor(k.Fg2).SingleLine().Shrink(1)
		})
	case inboxApproval:
		ap := it.approval
		if ap == nil {
			theme.UI.On(ui.Text(c, "等你处理")).TextColor(k.Fg2)
			return
		}
		ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, a.approvalIcon(ap.Kind)).FontSize(theme.IconRow).TextColor(k.AttnFg)
			title := ap.Title
			if ap.Kind == proto.ApprovalCommand {
				title = "想运行命令"
			}
			theme.UI.On(ui.Text(c, title)).TextColor(k.Fg2).SingleLine().Shrink(1).Tooltip(ap.Title)
		})
		if ap.Detail != "" {
			ui.Text(c, ap.Detail).Font(theme.Mono).FontSize(theme.Secondary.Size()).TextColor(k.Fg2).FontFeatures("liga=0").MaxLines(4).
				Padding(theme.Space1Half, theme.Space3).Radius(theme.RadiusCard).Background(k.Fill).FillWidth().Selectable()
		}
		ui.Row(c).Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
			for i, o := range inboxOptions(ap) {
				label := optionName(o)
				b := ui.Button(c.Key(o.ID), "").Label(label).Tooltip(o.Label).Height(theme.Row).Padding(0, theme.Space3, 0, theme.Space1Half).Gap(theme.Space1Half).
					Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(k.Fill)
				fg := k.Fg
				if i == 0 && o.Scope != proto.ScopeDeny {
					b.Background(k.Accent)
					fg = k.OnAccent
				}
				if b.Clicked() {
					a.inboxDecide(it, o)
				}
				b.Children(func() {
					keyCap(c, k, fmt.Sprint(i+1)).TextColor(fg)
					theme.UI.On(ui.Text(c, label)).TextColor(fg).SingleLine()
				})
			}
		})
	}
}

// offlineCard is a machine out of reach: for how long, a way to reconnect
// now, and what waited on me there, grayed.
func (a *App) offlineCard(c *ui.Context, k theme.Colors, it inboxItem) {
	ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.icons.Offline).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UIStrong.On(ui.Text(c, it.machine)).TextColor(k.Fg)
		gone := "正在重连"
		if !it.since.IsZero() {
			gone = "已断线 " + duration(c.Now().Sub(it.since)) + " · 正在重连"
			c.After(time.Second)
		}
		theme.Caption.On(ui.Text(c, gone)).TextColor(k.Muted)
		ui.Spacer(c)
		b := ui.Button(c, "").Label("立即重连").Height(theme.RowDense).Padding(0, theme.Space2, 0, theme.Space1Half).Gap(theme.Space1Half).Radius(theme.RadiusControl).
			Border(0, ui.Transparent).Background(k.Fill)
		if b.Clicked() {
			a.host.Reconnect()
		}
		b.Children(func() {
			ui.Icon(c, a.icons.Reconnect).FontSize(theme.IconRow).TextColor(k.Fg3)
			theme.Secondary.On(ui.Text(c, "立即重连")).TextColor(k.Fg)
		})
	})
	ui.Column(c).Padding(0, 0, 0, theme.IconRow+theme.Space2).Gap(theme.Space2).Children(func() {
		theme.UI.On(ui.Text(c, "重连后会话自动续上，期间的审批保留。")).TextColor(k.Fg2)
		for _, s := range it.waiting {
			ui.Row(c.Key(s.ID)).Padding(theme.Space2, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Opacity(0.55).Children(func() {
				a.harnessIcon(c, k, s.Harness.Kind, theme.IconRow)
				theme.UI.On(ui.Text(c, name(s))).TextColor(k.Fg).SingleLine().Shrink(1)
				theme.Caption.On(ui.Text(c, where(s))).TextColor(k.Muted).SingleLine().Shrink(1)
				ui.Spacer(c)
				theme.Caption.On(ui.Text(c, "等你")).TextColor(k.Muted)
			})
		}
	})
}

// inboxOptions are the choices an approval card offers, at most three in
// this order: allow once, allow for the session, deny; the harness's first
// three when it names none of them.
func inboxOptions(ap *proto.ApprovalRequested) []proto.ApprovalOption {
	if ap == nil {
		return nil
	}
	var out []proto.ApprovalOption
	for _, scope := range []proto.ApprovalScope{proto.ScopeOnce, proto.ScopeSession, proto.ScopeDeny} {
		if i := slices.IndexFunc(ap.Options, func(o proto.ApprovalOption) bool { return o.Scope == scope }); i >= 0 {
			out = append(out, ap.Options[i])
		}
	}
	if len(out) == 0 {
		out = ap.Options[:min(len(ap.Options), 3)]
	}
	return out
}

// optionName names an approval option in the UI's words by what it
// does; the harness's own label otherwise.
func optionName(o proto.ApprovalOption) string {
	switch o.Scope {
	case proto.ScopeOnce:
		return "允许一次"
	case proto.ScopeSession:
		return "本会话总是允许"
	case proto.ScopeAlways:
		return "总是允许"
	case proto.ScopeDeny:
		return "拒绝"
	}
	return o.Label
}

// inboxDecide answers an approval from its card, sending it to the card's
// machine, and leaves a line of what was decided; the selection moves on.
func (a *App) inboxDecide(it inboxItem, o proto.ApprovalOption) {
	if it.approval == nil || it.kind != inboxApproval {
		return
	}
	a.host.Respond(proto.RespondParams{Session: it.session.ID, Approval: it.approval.ID, Option: o.ID, Key: "desktop-" + rand.Text()})
	a.s.inboxDone[it.approval.ID] = "已" + optionName(o)
	a.s.inboxSel = max(a.s.inboxSel, 0) + 1
}

// pruneDone forgets the decisions shown for approvals that no session
// waits on any more.
func (a *App) pruneDone() {
	for id := range a.s.inboxDone {
		waits := false
		for _, aps := range a.s.inboxPending {
			if slices.ContainsFunc(aps, func(ap proto.ApprovalRequested) bool { return ap.ID == id }) {
				waits = true
				break
			}
		}
		if !waits {
			delete(a.s.inboxDone, id)
		}
	}
}

// openFromInbox opens a card's session.
func (a *App) openFromInbox(it inboxItem) {
	if it.kind != inboxOffline {
		a.Select(it.session.ID)
	}
}

// markReadAt clears a card's unread mark on its machine.
func (a *App) markReadAt(it inboxItem) {
	off := false
	a.host.Update(proto.UpdateParams{Session: it.session.ID, Unread: &off})
}

// where is a session's place on its machine: its directory, or 伙伴 for a
// companion.
func where(s proto.Session) string {
	if s.Mode == proto.ModePersonal {
		return "伙伴"
	}
	return tilde(s.Directory)
}

package shell

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

const (
	// chatCardMax is the widest an event row or a report card gets: 78% of
	// the chat column.
	chatCardMax = 0.78 * theme.PersonalColumn
	// chatAvatarRoom is what they leave for the avatar, in line with the
	// companion's bubbles.
	chatAvatarRoom = theme.Row + theme.Space2
)

// mainSession is the main session of the companion a Personal session
// belongs to: the session itself when it has no parent.
func (a *App) mainSession(s proto.Session) proto.Session {
	for s.Mode == proto.ModePersonal && s.Parent != "" {
		p, ok := a.cat.Get(s.Parent)
		if !ok {
			break
		}
		s = p
	}
	return s
}

// inChild reports whether a child session of a companion is what shows,
// s being the companion's main session.
func (a *App) inChild(s proto.Session) bool {
	cur, ok := a.current()
	return ok && cur.ID != s.ID && cur.Mode == proto.ModePersonal && a.mainSession(cur).ID == s.ID
}

// companionLink is what stands before a child session's title: its
// companion's name, a link back to the main session, and a chevron.
func (a *App) companionLink(c *ui.Context, k theme.Colors, main proto.Session) {
	l := theme.UI.On(ui.Text(c, name(main))).TextColor(k.Muted).SingleLine().Shrink(0).Label("回到 " + name(main))
	if l.Hovered() {
		l.TextColor(k.Fg2)
	}
	if l.Clicked() {
		a.Select(main.ID)
	}
	ui.Icon(c, a.icons.Expand).FontSize(theme.IconRow).TextColor(k.Faint)
}

// eventRow is a delegation, a handoff or a publication in a chat: a thin
// outline around one line, the whole of it a link.
func (a *App) eventRow(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	e := it.Event
	icon, verb, title := a.artifactIcon(e.Kind), "发布", e.Title
	switch e.Delegation {
	case proto.DelegatedSession:
		icon, verb = a.icons.Steer, "委派"
	case proto.HandedOff:
		icon, verb = a.icons.Code, "转交 Code"
	}
	if e.Session != "" {
		if t, ok := a.cat.Get(e.Session); ok {
			title = name(t)
		}
	}
	r := ui.Row(c).AlignSelf(ui.Start).MaxWidth(chatCardMax).Margin(0, 0, 0, chatAvatarRoom).Height(theme.RowStep).Padding(0, theme.Space3).
		Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Label(verb + " " + title)
	if r.Hovered() {
		r.Background(k.Fill)
	}
	if r.Clicked() {
		a.openEvent(s, e)
	}
	r.Children(func() {
		ui.Icon(c, icon).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, verb)).TextColor(k.Muted).SingleLine().Shrink(0)
		theme.UIMedium.On(ui.Text(c, title)).TextColor(k.Fg).SingleLine().Shrink(1)
		if e.Delegation == "" {
			ui.Textf(c, "v%d", e.Version).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Muted).Shrink(0)
		}
		ui.Icon(c, a.icons.Expand).FontSize(theme.IconRow).TextColor(k.Fg3)
	})
}

// openEvent goes where an event row leads: the session it made, in its
// own mode, or the artifact's tab in the right panel.
func (a *App) openEvent(s proto.Session, e *convo.Event) {
	if e.Session != "" {
		a.Select(e.Session)
		return
	}
	ps := a.panelFor(s.ID)
	art := proto.Artifact{ID: e.Artifact, Session: s.ID, Name: e.Title, Kind: e.Kind, Version: e.Version}
	if i := slices.IndexFunc(ps.arts.items, func(x proto.Artifact) bool { return x.ID == e.Artifact }); i >= 0 {
		art = ps.arts.items[i]
	}
	a.openArtifact(ps, art)
	a.bringPanel()
}

// reportCard is a report from a child or a handed-off Code session: how it
// went, where it came from and how the report begins, the whole card a
// link to that session. items are the chat's, for the title of a session
// the catalog no longer has.
func (a *App) reportCard(c *ui.Context, k theme.Colors, it convo.Item, items []convo.Item) {
	r := it.Report
	title, source := reportSource(a, r.From, items)
	label := reportLabel(r.Status)
	card := ui.Column(c).FillWidth().AlignSelf(ui.Start).MaxWidth(chatCardMax).Margin(0, 0, 0, chatAvatarRoom).Padding(theme.Space2, theme.Space3).
		Gap(theme.Space1).Radius(theme.RadiusCard).Background(k.Surface).Border(theme.LineWidth, k.Line).Label(title + " " + label)
	if card.Hovered() {
		card.Background(k.Fill)
	}
	if card.Clicked() {
		a.Select(r.From)
	}
	card.Children(func() {
		ui.Row(c).Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
			switch r.Status {
			case proto.ReportError:
				ui.Icon(c, a.icons.Failed).FontSize(theme.IconRow).TextColor(k.Del)
			case proto.ReportApproval:
				ui.Box(c).Size(theme.Dot, theme.Dot).Radius(theme.RadiusPill).Background(k.Attn)
			default:
				ui.Icon(c, a.icons.Check).FontSize(theme.IconRow).TextColor(k.Add)
			}
			theme.UIMedium.On(ui.Text(c, title)).TextColor(k.Fg).SingleLine().Shrink(1)
			theme.UI.On(ui.Text(c, "· "+label)).TextColor(k.Muted).SingleLine().Shrink(0)
			ui.Spacer(c)
			theme.Caption.On(ui.Text(c, source+" · "+clock(it.Time))).TextColor(k.Faint).SingleLine().Shrink(0)
			ui.Icon(c, a.icons.Expand).FontSize(theme.IconRow).TextColor(k.Fg3)
		})
		theme.Secondary.On(ui.Text(c, it.Text)).TextColor(k.Fg2).MaxLines(3)
	})
}

// reportLabel says how a report went.
func reportLabel(st proto.ReportStatus) string {
	switch st {
	case proto.ReportError:
		return "出错了"
	case proto.ReportApproval:
		return "等你审批"
	}
	return "完成"
}

// reportSource is a reporting session's title and what kind of session it
// is: from the catalog, else from the event that made it.
func reportSource(a *App, from proto.SessionID, items []convo.Item) (title, source string) {
	title, source = "子会话", "子会话"
	if s, ok := a.cat.Get(from); ok {
		if s.Mode == proto.ModeCode {
			source = "Code"
		}
		return name(s), source
	}
	for _, it := range items {
		if e := it.Event; e != nil && e.Session == from {
			if e.Delegation == proto.HandedOff {
				source = "Code"
			}
			if e.Title != "" {
				title = e.Title
			}
		}
	}
	return title, source
}

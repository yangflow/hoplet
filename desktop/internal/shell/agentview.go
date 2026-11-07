package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// agentView draws a subagent's record in the viewer, read only: a top row
// with its state, type, title and time; the task the main agent gave it,
// on a rule; its work groups as the conversation draws them; and what it
// handed back, or why it failed. While it runs, a working row ends it.
func (a *App) agentView(c *ui.Context, k theme.Colors, s proto.Session, id string) {
	g, _ := a.conv(s.ID).Agent(id)
	rec := a.record(s.ID, id)
	rec.Live(g.Status == proto.SubagentRunning)
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(theme.Space4).Gap(theme.Space3).Children(func() {
			a.agentHead(c, k, g)
			if more, first := rec.More(); more && a.older(c, k) {
				a.host.Older(proto.EventsParams{Session: s.ID, Agent: id, BeforeSeq: first}, func(p proto.EventsPage) { rec.Prepend(p) })
			}
			if task := rec.Task(); task != "" {
				ui.Column(c).Gap(theme.Space1).Children(func() {
					theme.Caption.On(ui.Text(c, "任务")).FontWeight(theme.UIStrong.Weight()).TextColor(k.Muted)
					theme.SecondaryBlock.On(ui.Text(c, task)).TextColor(k.Fg2).Padding(0, 0, 0, theme.Space3).
						BorderWidth(0, 0, 0, 2*theme.LineWidth).BorderColor(k.Line2).Selectable()
				})
			}
			items := rec.Grouped()
			answered := false
			for _, it := range items {
				switch it.Kind {
				case convo.KindWork:
					ui.Column(c.Key("in:" + it.Key)).FillWidth().Transition(enterMotion).Children(func() { a.work(c.Key(it.Key), k, s, it) })
					answered = false
				case convo.KindText:
					ui.Column(c.Key("in:" + it.Key)).FillWidth().Transition(enterMotion).Children(func() { a.text(c.Key(it.Key), k, it) })
					answered = true
				}
			}
			switch g.Status {
			case proto.SubagentRunning:
				ui.Row(c).Height(theme.RowDense).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
					squares(c, k, c.Preferences().ReduceMotion)
					theme.UI.On(ui.Text(c, "运行中")).TextColor(k.Fg3)
				})
			case proto.SubagentFailed:
				why := g.Summary
				if why == "" {
					why = "harness 没给出原因"
				}
				a.item(c.Key("failed"), k, s, convo.Item{Kind: convo.KindError, Text: "子代理出错结束：" + why})
			case proto.SubagentDone:
				if !answered && g.Summary != "" {
					a.text(c.Key("summary"), k, convo.Item{Key: "summary:" + id, Text: g.Summary})
				}
			}
		})
	})
}

// agentHead is a record's top row: the subagent's state, its type, its
// title and how long it ran.
func (a *App) agentHead(c *ui.Context, k theme.Colors, g convo.Subagent) {
	kind := g.Type
	if kind == "" {
		kind = "子代理"
	}
	ui.Row(c).FillWidth().Height(theme.RowStep).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		switch g.Status {
		case proto.SubagentRunning:
			ui.Spinner(c).Size(theme.Icon, theme.Icon)
		default:
			ui.Icon(c, a.agentIcon(g.Status)).FontSize(theme.Icon).TextColor(k.Fg3)
		}
		theme.UI.On(ui.Text(c, kind)).TextColor(k.Fg2).SingleLine().Shrink(0)
		theme.UIMedium.On(ui.Text(c, g.Title)).TextColor(k.Fg).SingleLine().Shrink(1)
		ui.Spacer(c)
		if !g.Start.IsZero() {
			ui.Text(c, agentTime(c, g)).FontSize(theme.Caption.Size()).TextColor(k.Faint).SingleLine().Shrink(0)
		}
	})
}

// agentIcon is a finished subagent's state: done, failed or stopped.
func (a *App) agentIcon(st proto.SubagentStatus) *ui.SVG {
	switch st {
	case proto.SubagentFailed:
		return a.icons.Close
	case proto.SubagentStopped:
		return a.icons.Stop
	}
	return a.icons.Check
}

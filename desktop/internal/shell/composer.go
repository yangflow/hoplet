package shell

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// composer is the box I write in under the conversation. An approval or a
// question takes over its top in place; the line I write in stays.
func (a *App) composer(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo) {
	h, _ := a.harness(s.Harness.Kind)
	running := cv.Running()
	var ap *proto.ApprovalRequested
	pending := cv.Pending()
	for i := range pending {
		if !a.s.hidden[pending[i].ID] {
			ap = &pending[i]
			break
		}
	}
	q, asked := cv.Question()
	a.queueTray(c, k, s, cv, h.Capabilities.Steer)
	// It floats over the conversation: glass in dark; in light, a blur
	// under the glass fill, as the glass would darken it.
	frame := ui.Column(c).FillWidth().Radius(theme.RadiusComposer).Border(1, k.GlassLine).Shadow(0, 10, 30, 0, k.Shadow)
	fill := ui.Transparent
	if k.Dark {
		frame.Material(glass.Glass{})
	} else {
		frame.Material(glass.Blur{Radius: frostRadius})
		fill = k.Glass
	}
	switch {
	case ap != nil || asked:
		frame.Border(1, k.AttnLine).Shadow(0, 0, 0, 3, k.AttnGlow)
	case !a.connected:
		frame.Border(1, k.Line2).BorderStyle(ui.BorderDashed)
	}
	frame.Children(func() {
		ui.Column(c).AlignSelf(ui.Stretch).Padding(12, 12, 8, 14).Gap(8).Radius(theme.RadiusComposer).Background(fill).Children(func() { a.composerBody(c, k, s, cv, h, running, ap, pending, q, asked) })
	})
}

// composerBody is what the composer holds: an approval or a question with
// the input under it, or the context line, the input and the controls.
func (a *App) composerBody(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo, h proto.HarnessInfo, running bool,
	ap *proto.ApprovalRequested, pending []proto.ApprovalRequested, q proto.Question, asked bool) {
	switch {
	case ap != nil:
		a.approvalBody(c, k, s, *ap, len(pending))
	case asked:
		a.questionBody(c, k, q)
	case !a.connected:
		a.offlineLine(c, k, false)
	case s.Mode == proto.ModeCode && s.Directory != "":
		a.contextLine(c, k, s)
	}
	if ap != nil || asked {
		// The input stays, on a line of its own with put away and
		// confirm the highlighted option.
		ui.Row(c).Padding(8, 0, 0, 0).Gap(4).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
			ui.Box(c).Grow(1).Children(func() { a.input(c, s, cv, ap, q, asked) })
			if ap != nil && iconButton(c, k, a.icons.Close, "收起 ⎋", false).Size(26, 26).Clicked() {
				a.s.hidden[ap.ID] = true
			}
			if round(c, k, a.icons.Send, "确认所选 ↩", true).Clicked() {
				if ap != nil {
					a.decide(s, *ap, a.s.drafts[s.ID])
				} else {
					a.answer(s, q, a.s.drafts[s.ID])
				}
				a.s.drafts[s.ID] = ""
			}
		})
		a.optionKeys(c, s, ap, q)
		return
	}
	a.input(c, s, cv, ap, q, asked)
	a.controls(c, k, s, h, running)
}

// contextLine is a Code composer's first line, all icons and plain text:
// the working directory, the branch and how many repositories it holds,
// from the session's diff.
func (a *App) contextLine(c *ui.Context, k theme.Colors, s proto.Session) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(theme.IconSizeInRow).TextColor(k.Muted)
		ui.Text(c, tilde(s.Directory)).Font(theme.Mono).FontSize(12).TextColor(k.Muted).SingleLine().Tooltip(s.Directory)
		d := a.diff(s)
		if d == nil {
			return
		}
		var branch string
		var repos int
		for _, r := range d.Repos {
			if r.State == proto.RepoMissing {
				continue
			}
			repos++
			if branch == "" || r.Path == "." {
				branch = r.Branch
			}
		}
		if branch != "" {
			ui.Icon(c, a.icons.Task).FontSize(theme.IconSizeInRow).TextColor(k.Muted)
			ui.Text(c, branch).Font(theme.Mono).FontSize(12).TextColor(k.Muted).SingleLine()
		}
		if repos > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Tooltip(fmt.Sprintf("%d 个仓库", repos)).Children(func() {
				ui.Icon(c, a.icons.Repos).FontSize(theme.IconSizeInRow).TextColor(k.Muted)
				theme.Secondary.On(ui.Textf(c, "%d", repos)).TextColor(k.Muted)
			})
		}
	})
}

// offlineLine is the first line while the daemon is out of reach: the
// machine and how long it has been gone, or, on the new-session page,
// that it is not connected; and reconnect now.
func (a *App) offlineLine(c *ui.Context, k theme.Colors, newPage bool) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.icons.Offline).FontSize(theme.IconSizeInRow).TextColor(k.Muted)
		machine := a.machine
		if machine == "" {
			machine = "本机"
		}
		status := "未连接"
		if !newPage {
			status = duration(c.Now().Sub(a.s.offlineSince))
			c.After(time.Second)
		}
		theme.Secondary.On(ui.Text(c, machine+" · "+status)).TextColor(k.Muted).Grow(1)
		if iconButton(c, k, a.icons.Reconnect, "立即重连", false).Size(24, 24).Clicked() {
			a.host.Reconnect()
		}
	})
}

// tilde writes a path under the home directory with ~.
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// optionKeys choose an option of the approval or question shown by its
// number, while the focus is not in the input (which types them).
func (a *App) optionKeys(c *ui.Context, s proto.Session, ap *proto.ApprovalRequested, q proto.Question) {
	n := len(q.Options)
	if ap != nil {
		n = len(ap.Options)
	}
	keys := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}
	for i := 0; i < n && i < len(keys); i++ {
		if c.Shortcut(0, keys[i]) {
			a.s.pick = i
			if ap != nil {
				a.decide(s, *ap, "")
			} else {
				a.answer(s, q, "")
			}
		}
	}
}

// input is the line I write in. ↩ sends (into the queue while a turn
// runs), ⇧↩ breaks the line, ⌘↩ steers the running turn. With an approval
// shown, ↩ chooses the highlighted option, or denies with what I wrote;
// with a question, ↩ answers with the option or what I wrote.
func (a *App) input(c *ui.Context, s proto.Session, cv *convo.Convo, ap *proto.ApprovalRequested, q proto.Question, asked bool) {
	draft := a.s.drafts[s.ID]
	hint := "回复"
	switch {
	case !a.connected:
		hint = "消息会在重连后发送"
	case ap != nil:
		hint = "或者告诉它该怎么做…"
	case asked:
		hint = "或者直接回答…"
	case s.Mode == proto.ModePersonal:
		hint = "和 " + name(s) + " 聊…"
	case len(cv.Items()) == 0:
		hint = "要做什么？"
	}
	in := ui.TextArea(c, &draft).Lines(1, 8).MinHeight(0).Placeholder(hint).Label("消息").Font(ui.Theme{}.Font).
		FontSize(theme.Reading.Size()).Background(ui.Transparent).Border(0, ui.Transparent).Padding(0)
	in.OnTextKey(func(key ui.TextKey) bool {
		text := key.Text()
		switch {
		case key.Key == ui.KeyEnter && key.Mods == 0:
			switch {
			case ap != nil:
				a.decide(s, *ap, text)
			case asked:
				a.answer(s, q, text)
			case text == "":
				return true
			default:
				a.send(s, text)
			}
			key.SetText("")
			a.s.drafts[s.ID] = ""
			return true
		case key.Key == ui.KeyEnter && key.Mods == ui.Cmd && text != "" && cv.Running():
			a.host.Steer(proto.SteerParams{Session: s.ID, Text: text})
			key.SetText("")
			a.s.drafts[s.ID] = ""
			return true
		case (key.Key == ui.KeyUp || key.Key == ui.KeyDown) && key.Mods == 0 && (ap != nil || asked):
			n := len(q.Options)
			if ap != nil {
				n = len(ap.Options)
			}
			if n > 0 {
				d := 1
				if key.Key == ui.KeyUp {
					d = -1
				}
				a.s.pick = (a.s.pick + d + n) % n
			}
			return true
		case key.Key == ui.KeyEscape && key.Mods == 0:
			switch {
			case ap != nil:
				a.s.hidden[ap.ID] = true
			case cv.Running():
				a.stopTurn(s)
			}
			return true
		}
		return false
	})
	a.s.drafts[s.ID] = draft
}

// stopTwice is how soon a second ⎋ must come to stop the turn.
const stopTwice = 2 * time.Second

// stopTurn stops the running turn on the second ⎋ within stopTwice.
func (a *App) stopTurn(s proto.Session) {
	now := time.Now()
	if now.Sub(a.s.armed) < stopTwice {
		a.s.armed = time.Time{}
		a.host.Interrupt(s.ID)
		return
	}
	a.s.armed = now
	a.hint("再按一次 ⎋ 停止")
}

// decide answers an approval: with what I wrote, it denies and sends the
// text as my next message; else it takes the highlighted option.
func (a *App) decide(s proto.Session, ap proto.ApprovalRequested, text string) {
	opt := ""
	if text != "" {
		for _, o := range ap.Options {
			if o.Scope == proto.ScopeDeny {
				opt = o.ID
			}
		}
	} else if a.s.pick < len(ap.Options) {
		opt = ap.Options[a.s.pick].ID
	}
	if opt == "" {
		return
	}
	a.host.Respond(proto.RespondParams{Session: s.ID, Approval: ap.ID, Option: opt, Key: "desktop-" + rand.Text()})
	a.s.pick = 0
	if text != "" {
		a.send(s, text)
	}
}

// answer answers a question with what I wrote, else the highlighted
// option.
func (a *App) answer(s proto.Session, q proto.Question, text string) {
	if text == "" && a.s.pick < len(q.Options) {
		text = q.Options[a.s.pick]
	}
	if text == "" {
		return
	}
	a.host.Answer(proto.AnswerParams{Session: s.ID, Question: q.ID, Answer: text})
	a.s.pick = 0
}

// approvalBody shows an approval in the composer: its kind and title with
// the count, what it is about, and its numbered options, the highlighted
// one with ↩.
func (a *App) approvalBody(c *ui.Context, k theme.Colors, s proto.Session, ap proto.ApprovalRequested, n int) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.approvalIcon(ap.Kind)).FontSize(theme.IconSize).TextColor(k.AttnFg)
		title := ap.Title
		if ap.Kind == proto.ApprovalCommand {
			title = "运行命令" // the command itself shows below
		}
		theme.UIStrong.On(ui.Text(c, title)).TextColor(k.AttnFg).SingleLine().Grow(1).Shrink(1).Tooltip(ap.Title)
		ui.Textf(c, "1 / %d", n).Font(theme.Mono).FontSize(11).TextColor(k.Muted)
	})
	if ap.Detail != "" {
		ui.Text(c, ap.Detail).Font(theme.Mono).FontSize(12.5).TextColor(k.Fg2).FontFeatures("liga=0").MaxLines(8).
			Padding(8, 10).Radius(theme.RadiusCard).Background(k.Fill).FillWidth().Selectable()
	}
	a.options(c, k, len(ap.Options), func(i int) string { return ap.Options[i].Label }, func(i int) {
		a.s.pick = i
		a.decide(s, ap, "")
	})
}

// questionBody shows a question in the composer with its options.
func (a *App) questionBody(c *ui.Context, k theme.Colors, q proto.Question) {
	theme.UIStrong.On(ui.Text(c, q.Text)).TextColor(k.Fg)
	a.options(c, k, len(q.Options), func(i int) string { return q.Options[i] }, func(i int) { a.s.pick = i })
}

// options lists numbered choices, 32 high with a key cap; the highlighted
// one has a light background and ↩.
func (a *App) options(c *ui.Context, k theme.Colors, n int, label func(int) string, choose func(int)) {
	ui.Column(c).Gap(2).Children(func() {
		for i := 0; i < n; i++ {
			r := ui.Row(c.Key(i)).Height(32).Padding(0, 10, 0, 6).Gap(10).AlignItems(ui.Center).Radius(9).Label(label(i))
			on := i == a.s.pick
			if on {
				r.Background(k.Sel)
			}
			if r.Clicked() {
				choose(i)
			}
			r.Children(func() {
				ui.Box(c).Size(20, 20).Radius(5).Border(1, k.Line).Background(k.Bg).Center().Children(func() {
					theme.Caption.On(ui.Textf(c, "%d", i+1)).TextColor(k.Muted)
				})
				theme.UI.On(ui.Text(c, label(i))).TextColor(k.Fg).Grow(1)
				if on {
					theme.Caption.On(ui.Text(c, "↩")).TextColor(k.Muted)
				}
			})
		}
	})
}

func (a *App) approvalIcon(kind proto.ApprovalKind) *ui.SVG {
	switch kind {
	case proto.ApprovalCommand:
		return a.icons.Run
	case proto.ApprovalFileChange:
		return a.icons.Edit
	}
	return a.icons.Confirm
}

// controls is the composer's bottom row: the permission mode and plan on
// the left; the effort and model pill and send (stop and steer while a
// turn runs) on the right.
func (a *App) controls(c *ui.Context, k theme.Colors, s proto.Session, h proto.HarnessInfo, running bool) {
	st := runSettings{harness: s.Harness.Kind, perm: s.Permission, plan: s.Plan, effort: s.Effort, fast: s.Fast, model: s.Model}
	set := a.sessionSink(s)
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		if s.Mode == proto.ModeCode {
			a.modeControl(c, k, st, h.Capabilities, set)
		}
		ui.Spacer(c)
		a.effortPill(c, k, st, []proto.HarnessInfo{h}, set)
		draft := a.s.drafts[s.ID]
		if running {
			if h.Capabilities.Steer && draft != "" && iconButton(c, k, a.icons.Steer, "插话 ⌘↩", false).Clicked() {
				a.host.Steer(proto.SteerParams{Session: s.ID, Text: draft})
				a.s.drafts[s.ID] = ""
			}
			if round(c, k, a.icons.Stop, "停止 ⎋", true).Clicked() {
				a.host.Interrupt(s.ID)
			}
			return
		}
		if round(c, k, a.icons.Send, "发送 ↩", draft != "").Clicked() && draft != "" {
			a.send(s, draft)
			a.s.drafts[s.ID] = ""
		}
	})
}

// sessionSink applies a session's settings through the daemon.
func (a *App) sessionSink(s proto.Session) settingsSink {
	return settingsSink{
		perm: func(m proto.PermissionMode) {
			a.host.SetPermission(proto.SetPermissionParams{Session: s.ID, Permission: m})
		},
		plan:   func(on bool) { a.host.SetPlan(proto.SetPlanParams{Session: s.ID, Plan: on}) },
		effort: func(e proto.Effort) { a.host.SetEffort(proto.SetEffortParams{Session: s.ID, Effort: e}) },
		fast:   func(on bool) { a.host.SetFast(proto.SetFastParams{Session: s.ID, Fast: on}) },
		model: func(_ string, m proto.ModelInfo) {
			a.host.SetModel(proto.SetModelParams{Session: s.ID, Model: m.ID, Provider: m.Provider})
		},
	}
}

// round is the round send or stop button; off grays it.
func round(c *ui.Context, k theme.Colors, icon *ui.SVG, tip string, on bool) ui.Element {
	b := ui.Button(c, "").Label(tip).Tooltip(tip).Size(theme.RowHeight, theme.RowHeight).Padding(0).
		Radius(theme.RadiusPill).Border(0, ui.Transparent).Background(k.Accent).Disabled(!on)
	color := k.OnAccent
	if !on {
		b.Background(k.Fill2)
		color = k.Faint
	}
	return b.Children(func() { ui.Icon(c, icon).FontSize(theme.IconSize).TextColor(color) })
}

// queueTray shows the messages waiting for the turn to end, and those
// written while offline, in a tray on top of the composer: steer all and
// clear, then each message with a handle to drag it to another place,
// steer, edit and delete.
func (a *App) queueTray(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo, steer bool) {
	queue := cv.Queue()
	outbox := a.outbox(s.ID)
	if len(queue) == 0 && len(outbox) == 0 {
		return
	}
	ui.Column(c).FillWidth().Margin(0, 12).Radius(12, 12, 0, 0).Border(1, k.Line).BorderWidth(1, 1, 0, 1).Background(k.Bg2).Children(func() {
		ui.Row(c).Padding(4, 6, 4, 12).Gap(2).AlignItems(ui.Center).Children(func() {
			theme.Caption.On(ui.Textf(c, "排队 %d 条", len(queue)+len(outbox))).TextColor(k.Muted).Grow(1)
			if steer && len(queue) > 0 && iconButton(c, k, a.icons.Steer, "全部插话", false).Size(24, 24).Clicked() {
				for _, m := range queue {
					a.host.Steer(proto.SteerParams{Session: s.ID, Text: m.Text})
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			}
			if len(queue) > 0 && iconButton(c, k, a.icons.Close, "清空", false).Size(24, 24).Clicked() {
				for _, m := range queue {
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			}
		})
		a.s.queue.Reorder = func(rows []int, to int) {
			if len(rows) != 1 || rows[0] >= len(queue) {
				return
			}
			if to > rows[0] {
				to-- // the place counts the row being moved
			}
			a.host.MoveQueued(proto.QueueMoveParams{Session: s.ID, Key: queue[rows[0]].Key, Position: to})
		}
		ui.List(c, &a.s.queue, len(queue), func(i int) {
			m := queue[i]
			ui.Row(c.Key(m.Key)).Padding(2, 6, 2, 8).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
				ui.Icon(c, a.icons.Grip).FontSize(theme.IconSizeInRow).TextColor(k.Faint).Label("拖动排序")
				label := m.Text
				if m.Error != "" {
					label = m.Error + " · " + m.Text
				}
				theme.UI.On(ui.Text(c, label)).TextColor(k.Fg2).SingleLine().Grow(1).Shrink(1)
				if steer && iconButton(c, k, a.icons.Steer, "插话", false).Size(24, 24).Clicked() {
					a.host.Steer(proto.SteerParams{Session: s.ID, Text: m.Text})
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
				if iconButton(c, k, a.icons.Edit, "编辑", false).Size(24, 24).Clicked() {
					a.s.drafts[s.ID] = m.Text
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
				if iconButton(c, k, a.icons.Close, "删除", false).Size(24, 24).Clicked() {
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			})
		}).FillWidth().MaxHeight(3 * 30)
		for i, t := range outbox {
			ui.Row(c.Key(fmt.Sprintf("out:%d", i))).Padding(2, 6, 2, 8).Gap(8).MinHeight(28).AlignItems(ui.Center).
				BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
				theme.UI.On(ui.Text(c, t)).TextColor(k.Fg2).SingleLine().Grow(1).Shrink(1)
				theme.Caption.On(ui.Text(c, "重连后发送")).TextColor(k.Muted)
			})
		}
	})
}

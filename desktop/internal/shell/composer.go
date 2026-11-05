package shell

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// composer is what I write in under the conversation, in three layers:
// the context row (Code), then one box holding the queue on top and what
// I write below. An approval or a question takes over the lower part in
// place, the queue folded to a line; the line I write in stays.
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
	if s.Mode == proto.ModeCode && s.Directory != "" {
		a.contextRow(c, k, s)
	}
	frame := ui.Column(c).FillWidth().Radius(theme.RadiusComposer).Clip().
		Background(k.Glass).Border(theme.LineWidth, k.GlassLine).Transition(ui.ElementTransition{Size: true, Animation: &theme.MotionSmooth})
	k.ShadowComposer.On(frame)
	switch {
	case ap != nil || asked:
		// Needs you: the outline turns, and the glow grows out to twice its
		// width once, then settles.
		id := "q:" + q.ID
		if ap != nil {
			id = ap.ID
		}
		ring := k.RingAttn
		if a.s.ringPeaked != id {
			ring.Spread = frame.AnimateTo("ring", 2*k.RingAttn.Spread, theme.MotionPulse)
			if frame.Arrived("ring") {
				a.s.ringPeaked = id
				c.AnimationFrame()
			}
		} else {
			ring.Spread = frame.AnimateTo("ring", k.RingAttn.Spread, theme.MotionSmooth)
		}
		ring.On(frame.Border(theme.LineWidth, k.AttnLine))
	case !a.connected:
		frame.Border(theme.LineWidth, k.Line2).BorderStyle(ui.BorderDashed)
	}
	frame.Children(func() {
		a.queue(c, k, s, cv, h.Capabilities.Steer, ap != nil || asked)
		ui.Column(c).Padding(theme.Space3, theme.Space3, theme.Space2).Gap(theme.Space2).Children(func() {
			a.composerBody(c, k, s, cv, h, running, ap, pending, q, asked)
		})
	})
}

// composerBody is what the composer's lower part holds: an approval or a
// question with the input under it, or the input and the controls.
func (a *App) composerBody(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo, h proto.HarnessInfo, running bool,
	ap *proto.ApprovalRequested, pending []proto.ApprovalRequested, q proto.Question, asked bool) {
	switch {
	case ap != nil:
		a.approvalBody(c, k, s, *ap, len(pending))
	case asked:
		a.questionBody(c, k, q)
	}
	if ap != nil || asked {
		// The input stays, on a line of its own with put away and
		// confirm the highlighted option.
		ui.Row(c).Padding(theme.Space2, 0, 0, 0).Gap(theme.Space1).AlignItems(ui.Center).BorderWidth(theme.LineWidth, 0, 0, 0).BorderColor(k.Line).Children(func() {
			ui.Box(c).Grow(1).Children(func() { a.input(c, s, cv, ap, q, asked) })
			if ap != nil && iconButton(c, k, a.icons.Close, "收起 ⎋", false).Size(theme.RowGroup, theme.RowGroup).Clicked() {
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
	a.controls(c, k, s, cv, h, running)
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
	in := ui.TextArea(c, &draft).Lines(2, 8).MinHeight(0).Placeholder(hint).Label("消息").Font(ui.Theme{}.Font).
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
	ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.approvalIcon(ap.Kind)).FontSize(theme.Icon).TextColor(k.AttnFg)
		title := ap.Title
		if ap.Kind == proto.ApprovalCommand {
			title = "运行命令" // the command itself shows below
		}
		theme.UIStrong.On(ui.Text(c, title)).TextColor(k.AttnFg).SingleLine().Grow(1).Shrink(1).Tooltip(ap.Title)
		ui.Textf(c, "1 / %d", n).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Muted)
	})
	if ap.Detail != "" {
		theme.Code.On(ui.Text(c, ap.Detail)).TextColor(k.Fg2).MaxLines(8).
			Padding(theme.Space1Half, theme.Space3).Radius(theme.RadiusCard).Background(k.Fill).FillWidth().Selectable()
	}
	a.options(c, k, len(ap.Options), func(i int) string { return optionName(ap.Options[i]) }, func(i int) {
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
	ui.Column(c).Gap(theme.SpaceHalf).Children(func() {
		for i := 0; i < n; i++ {
			r := ui.Row(c.Key(i)).Height(theme.RowStep).Padding(0, theme.Space2, 0, theme.Space1Half).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).Label(label(i))
			on := i == a.s.pick
			if on {
				r.Background(k.Selected)
			}
			if r.Clicked() {
				choose(i)
			}
			r.Children(func() {
				keyCap(c, k, fmt.Sprint(i+1))
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

// controls is the composer's bottom row of ghost buttons: the model and
// effort (with Plan, Fast and Ultracode), the permission mode on the left; steer while a turn
// runs and send (stop while it runs) on the right, after the context
// ring. Offline, what I send waits for the connection, which the row says.
func (a *App) controls(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo, h proto.HarnessInfo, running bool) {
	st := runSettings{harness: s.Harness.Kind, perm: s.Permission, plan: s.Plan, effort: s.Effort, fast: s.Fast, ultracode: s.Ultracode, model: s.Model}
	set := a.sessionSink(s)
	ui.Row(c).Gap(theme.SpaceHalf).AlignItems(ui.Center).Children(func() {
		a.runButton(c, k, st, []proto.HarnessInfo{h}, set)
		if s.Mode == proto.ModeCode {
			a.modeControl(c, k, st, h.Capabilities, set)
		}
		ui.Spacer(c)
		draft := a.s.drafts[s.ID]
		if !a.connected {
			theme.Caption.On(ui.Text(c, "重连后发送")).TextColor(k.Muted).Margin(0, theme.Space1Half)
		}
		if u, ok := cv.Context(); ok {
			a.contextRing(c, k, u)
		}
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
		ultracode: func(on bool) {
			a.host.SetUltracode(proto.SetUltracodeParams{Session: s.ID, Ultracode: on})
		},
		model: func(_ string, m proto.ModelInfo) {
			a.host.SetModel(proto.SetModelParams{Session: s.ID, Model: m.ID, Provider: m.Provider})
		},
	}
}

// round is the round send or stop button; off grays it.
func round(c *ui.Context, k theme.Colors, icon *ui.SVG, tip string, on bool) ui.Element {
	b := ui.Button(c, "").Label(tip).Tooltip(tip).Size(theme.Row, theme.Row).Padding(0).
		Radius(theme.RadiusPill).Border(0, ui.Transparent).Background(k.Accent).Disabled(!on)
	color := k.OnAccent
	if !on {
		b.Background(k.Hover)
		color = k.Faint
	}
	return b.Children(func() { ui.Icon(c, icon).FontSize(theme.Icon).TextColor(color) })
}

// queueRows is how many queued messages show before the rest fold.
const queueRows = 3

// queue is the top of the composer while messages wait: those queued for
// the turn to end and those written while offline. A title with steer
// all and clear, then up to three messages, each with a handle to drag it
// to another place and, under the pointer, steer, edit and delete; the
// rest fold into 还有 N 条. While an approval or a question takes the
// composer, it is one line.
func (a *App) queue(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo, steer, folded bool) {
	queue := cv.Queue()
	outbox := a.outbox(s.ID)
	n := len(queue) + len(outbox)
	if n == 0 {
		return
	}
	ui.Column(c).Padding(theme.Space1, 0, theme.Space1, 0).BorderWidth(0, 0, theme.LineWidth, 0).BorderColor(k.Line).Children(func() {
		if folded {
			var first string
			if len(queue) > 0 {
				first = queue[0].Text
			} else {
				first = outbox[0]
			}
			ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Plan).FontSize(theme.IconRow).TextColor(k.Fg3)
				theme.Secondary.On(ui.Textf(c, "排队 %d 条", n)).TextColor(k.Fg3)
				theme.Secondary.On(ui.Text(c, "· 下一条："+first)).TextColor(k.Faint).SingleLine().Shrink(1)
			})
			return
		}
		ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space1, 0, theme.Space3).Gap(theme.SpaceHalf).AlignItems(ui.Center).Children(func() {
			theme.Caption.On(ui.Textf(c, "排队 %d", n)).FontWeight(theme.UIStrong.Weight()).TextColor(k.Muted).Grow(1)
			if steer && len(queue) > 0 && iconButton(c, k, a.icons.Steer, "全部插话", false).Size(theme.RowDense, theme.RowDense).Clicked() {
				for _, m := range queue {
					a.host.Steer(proto.SteerParams{Session: s.ID, Text: m.Text})
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			}
			if len(queue) > 0 && iconButton(c, k, a.icons.Close, "清空", false).Size(theme.RowDense, theme.RowDense).Clicked() {
				for _, m := range queue {
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			}
		})
		shown := len(queue)
		if !a.s.queueAll && shown > queueRows {
			shown = queueRows
		}
		a.s.queue.Reorder = func(rows []int, to int) {
			if len(rows) != 1 || rows[0] >= len(queue) {
				return
			}
			if to > rows[0] {
				to-- // the place counts the row being moved
			}
			a.host.MoveQueued(proto.QueueMoveParams{Session: s.ID, Key: queue[rows[0]].Key, Position: to})
		}
		ui.List(c, &a.s.queue, shown, func(i int) {
			m := queue[i]
			r := ui.Row(c.Key(m.Key)).Height(theme.Row).Margin(0, theme.Space1).Padding(0, theme.Space1Half, 0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow)
			hovered := r.Hovered()
			if hovered {
				r.Background(k.Fill)
			}
			r.Children(func() {
				ui.Icon(c, a.icons.Grip).FontSize(theme.IconRow).TextColor(k.Faint).Label("拖动排序")
				label := m.Text
				if m.Error != "" {
					label = m.Error + " · " + m.Text
				}
				theme.UI.On(ui.Text(c, label)).TextColor(k.Fg2).SingleLine().Grow(1).Shrink(1)
				if !hovered {
					return
				}
				if steer && iconButton(c, k, a.icons.Steer, "插话", false).Size(theme.ControlXS, theme.ControlXS).Clicked() {
					a.host.Steer(proto.SteerParams{Session: s.ID, Text: m.Text})
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
				if iconButton(c, k, a.icons.Edit, "编辑", false).Size(theme.ControlXS, theme.ControlXS).Clicked() {
					a.s.drafts[s.ID] = m.Text
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
				if iconButton(c, k, a.icons.Close, "删除", false).Size(theme.ControlXS, theme.ControlXS).Clicked() {
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			})
		}).FillWidth()
		if more := len(queue) - shown; more > 0 {
			r := ui.Row(c).Height(theme.RowGroup).Padding(0, theme.Space3).Gap(theme.Space1Half).AlignItems(ui.Center).Label(fmt.Sprintf("还有 %d 条", more))
			if r.Clicked() {
				a.s.queueAll = true
			}
			r.Children(func() {
				theme.Caption.On(ui.Textf(c, "还有 %d 条", more)).TextColor(k.Muted)
				ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
			})
		}
		for i, t := range outbox {
			ui.Row(c.Key(fmt.Sprintf("out:%d", i))).Height(theme.Row).Margin(0, theme.Space1).Padding(0, theme.Space1Half, 0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				theme.UI.On(ui.Text(c, t)).TextColor(k.Fg2).SingleLine().Grow(1).Shrink(1)
				theme.Caption.On(ui.Text(c, "重连后发送")).TextColor(k.Muted)
			})
		}
	})
}

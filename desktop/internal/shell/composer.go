package shell

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"slices"
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
	box := ui.Column(c).FillWidth().Padding(12, 12, 8, 14).Gap(8).Radius(theme.RadiusComposer).
		Background(k.Glass).Border(1, k.GlassLine).Shadow(0, 2, 10, 0, k.Shadow).Material(glass.Glass{})
	switch {
	case ap != nil || asked:
		box.Border(1, k.AttnLine)
	case !a.connected:
		box.Border(1, k.Line2).BorderStyle(ui.BorderDashed)
	}
	box.Children(func() {
		switch {
		case ap != nil:
			a.approvalBody(c, k, s, *ap, len(pending))
		case asked:
			a.questionBody(c, k, q)
		case !a.connected:
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Offline).FontSize(theme.IconSizeInRow).TextColor(k.AttnFg)
				theme.Secondary.On(ui.Text(c, "断线 · 消息会在重连后发送")).TextColor(k.Muted)
			})
		case s.Mode == proto.ModeCode && s.Directory != "":
			ui.Row(c).Gap(6).Children(func() {
				chip(c, k, a.icons.Folder, filepath.Base(s.Directory), s.Directory)
			})
		}
		a.input(c, s, cv, ap, q, asked)
		a.controls(c, k, s, h, running)
	})
}

// chip is a small labeled tag of the composer's first line.
func chip(c *ui.Context, k theme.Colors, icon *ui.SVG, label, tip string) ui.Element {
	return ui.Row(c).Height(24).Padding(0, 8).Gap(4).AlignItems(ui.Center).Radius(theme.RadiusControl).
		Border(1, k.Line).Background(k.Fill).Tooltip(tip).Children(func() {
		ui.Icon(c, icon).FontSize(theme.IconSizeSmall).TextColor(k.Fg3)
		ui.Text(c, label).Font(theme.Mono).FontSize(12).TextColor(k.Fg3).SingleLine()
	})
}

// input is the line I write in. ↩ sends (into the queue while a turn
// runs), ⇧↩ breaks the line, ⌘↩ steers the running turn. With an approval
// shown, ↩ chooses the highlighted option, or denies with what I wrote;
// with a question, ↩ answers with the option or what I wrote.
func (a *App) input(c *ui.Context, s proto.Session, cv *convo.Convo, ap *proto.ApprovalRequested, q proto.Question, asked bool) {
	draft := a.s.drafts[s.ID]
	hint := "回复"
	switch {
	case ap != nil:
		hint = "或者告诉它该怎么做…"
	case asked:
		hint = "或者直接回答…"
	case s.Mode == proto.ModePersonal:
		hint = "和 " + name(s) + " 聊…"
	case len(cv.Items()) == 0:
		hint = "要做什么？"
	}
	in := ui.TextArea(c, &draft).Lines(1, 8).Placeholder(hint).Label("消息").Font(ui.Theme{}.Font).
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
	a.toast = "再按一次 ⎋ 停止"
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

// approvalBody shows an approval in the composer: its kind, title and
// count, what it is about, and its options, the highlighted one with ↩.
func (a *App) approvalBody(c *ui.Context, k theme.Colors, s proto.Session, ap proto.ApprovalRequested, n int) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.approvalIcon(ap.Kind)).FontSize(theme.IconSize).TextColor(k.AttnFg)
		theme.UIStrong.On(ui.Text(c, ap.Title)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1)
		if n > 1 {
			theme.Caption.On(ui.Textf(c, "1 / %d", n)).TextColor(k.Muted)
		}
		if iconButton(c, k, a.icons.Close, "收起 ⎋", false).Size(24, 24).Clicked() {
			a.s.hidden[ap.ID] = true
		}
	})
	if ap.Detail != "" {
		ui.Text(c, ap.Detail).Font(theme.Mono).FontSize(12).TextColor(k.Fg2).FontFeatures("liga=0").MaxLines(8).
			Padding(8, 10).Radius(theme.RadiusRow).Background(k.Fill).FillWidth().Selectable()
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

// options lists numbered choices; the highlighted one has a light
// background and ↩.
func (a *App) options(c *ui.Context, k theme.Colors, n int, label func(int) string, choose func(int)) {
	for i := 0; i < n; i++ {
		r := ui.Row(c.Key(i)).Height(30).Padding(0, 8).Gap(10).AlignItems(ui.Center).Radius(theme.RadiusRow).Label(label(i))
		on := i == a.s.pick
		if on {
			r.Background(k.Sel)
		}
		if r.Clicked() {
			choose(i)
		}
		r.Children(func() {
			ui.Box(c).Padding(0, 4).Radius(theme.RadiusXS).Border(1, k.Line2).Children(func() {
				theme.Caption.On(ui.Textf(c, "%d", i+1)).TextColor(k.Muted)
			})
			theme.UI.On(ui.Text(c, label(i))).TextColor(k.Fg).Grow(1)
			if on {
				theme.Caption.On(ui.Text(c, "↩")).TextColor(k.Muted)
			}
		})
	}
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
	caps := h.Capabilities
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		if s.Mode == proto.ModeCode && caps.SetMode {
			b := iconButton(c, k, a.modeIcon(s.Permission), "审批："+modeName(s.Permission), false)
			if b.Clicked() {
				a.s.permOpen = !a.s.permOpen
			}
			ui.Popover(c, b, &a.s.permOpen, func() { a.permissionMenu(c, k, s, caps) })
			if s.Plan && iconButton(c, k, a.icons.Plan, "Plan ⇧⇥", true).Clicked() {
				a.host.SetPlan(proto.SetPlanParams{Session: s.ID, Plan: false})
			}
		}
		ui.Spacer(c)
		a.effortPill(c, k, s, h)
		draft := a.s.drafts[s.ID]
		if running {
			if caps.Steer && draft != "" && iconButton(c, k, a.icons.Steer, "插话 ⌘↩", false).Clicked() {
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

// permissionModes are the modes in order, with what each does.
func permissionModes() []struct {
	mode proto.PermissionMode
	what string
} {
	return []struct {
		mode proto.PermissionMode
		what string
	}{
		{proto.ModeConfirm, "改动和命令都先问我"},
		{proto.ModeAutoEdit, "编辑直接改，命令仍问"},
		{proto.ModeAutoReview, "harness 判断风险，只问危险的"},
		{proto.ModeFullAuto, "什么都不问（慎用）"},
	}
}

func modeName(m proto.PermissionMode) string {
	switch m {
	case proto.ModeAutoEdit:
		return "自动编辑"
	case proto.ModeAutoReview:
		return "智能审批"
	case proto.ModeFullAuto:
		return "全自动"
	}
	return "需确认"
}

func (a *App) modeIcon(m proto.PermissionMode) *ui.SVG {
	switch m {
	case proto.ModeAutoEdit:
		return a.icons.AutoEdit
	case proto.ModeAutoReview:
		return a.icons.Review
	case proto.ModeFullAuto:
		return a.icons.FullAuto
	}
	return a.icons.Confirm
}

// permissionMenu lists the modes the harness has, then plan mode.
func (a *App) permissionMenu(c *ui.Context, k theme.Colors, s proto.Session, caps proto.Capabilities) {
	ui.Column(c).Width(280).Padding(6).Gap(2).Children(func() {
		for _, m := range permissionModes() {
			if !slices.Contains(caps.Modes, m.mode) {
				continue
			}
			r := menuRow(c.Key(m.mode), k, s.Permission == m.mode)
			if r.Clicked() {
				a.host.SetPermission(proto.SetPermissionParams{Session: s.ID, Permission: m.mode})
				a.s.permOpen = false
			}
			r.Children(func() {
				ui.Icon(c, a.modeIcon(m.mode)).FontSize(theme.IconSize).TextColor(k.Fg3)
				ui.Column(c).Grow(1).Children(func() {
					theme.UI.On(ui.Text(c, modeName(m.mode))).TextColor(k.Fg)
					theme.Caption.On(ui.Text(c, m.what)).TextColor(k.Muted)
				})
			})
		}
		if caps.PlanMode {
			ui.Box(c).Height(1).FillWidth().Background(k.Line).Margin(4, 0)
			ui.Row(c).Height(36).Padding(0, 8).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Plan).FontSize(theme.IconSize).TextColor(k.Fg3)
				ui.Column(c).Grow(1).Children(func() {
					theme.UI.On(ui.Text(c, "Plan")).TextColor(k.Fg)
					theme.Caption.On(ui.Text(c, "先给计划，我批准后才动手")).TextColor(k.Muted)
				})
				on := s.Plan
				if ui.Switch(c, &on).Label("Plan ⇧⇥").Changed() {
					a.host.SetPlan(proto.SetPlanParams{Session: s.ID, Plan: on})
				}
			})
		}
	})
}

// menuRow is a row of a popover list; on marks the current choice.
func menuRow(c *ui.Context, k theme.Colors, on bool) ui.Element {
	r := ui.Row(c).Height(40).Padding(0, 8).Gap(10).AlignItems(ui.Center).Radius(theme.RadiusRow)
	switch {
	case on:
		r.Background(k.Sel)
	case r.Hovered():
		r.Background(k.Fill)
	}
	return r
}

// effortPill shows the effort as bars, the harness and the model; it opens
// the effort and model popover.
func (a *App) effortPill(c *ui.Context, k theme.Colors, s proto.Session, h proto.HarnessInfo) {
	model, _ := currentModel(s, h)
	b := ui.Button(c, "").Label("强度与模型").Tooltip(fmt.Sprintf("%s · %s · 强度 %s", h.Kind, model, effortName(s.Effort))).
		Height(theme.RowHeight).Padding(0, 10).Gap(6).Radius(theme.RadiusPill).Border(1, k.Line).Background(ui.Transparent)
	if b.Clicked() {
		a.s.effortOpen = !a.s.effortOpen
		if a.s.effortOpen {
			a.host.Harnesses(a.SetHarnesses)
		}
	}
	b.Children(func() {
		bars(c, k, s.Effort)
		a.harnessIcon(c, k, s.Harness.Kind, theme.IconSizeInRow)
		theme.Secondary.On(ui.Text(c, model)).TextColor(k.Fg3).SingleLine()
		ui.Icon(c, a.icons.Menu).FontSize(theme.IconSizeSmall).TextColor(k.Faint)
	})
	ui.Popover(c, b, &a.s.effortOpen, func() { a.effortMenu(c, k, s, h) })
}

func effortName(e proto.Effort) string {
	switch e {
	case proto.EffortLow:
		return "低"
	case proto.EffortMedium:
		return "中"
	case proto.EffortHigh:
		return "高"
	case proto.EffortXHigh:
		return "很高"
	case proto.EffortMax:
		return "最高"
	case proto.EffortUltra:
		return "极限"
	}
	return "默认"
}

// bars draws the effort as three bars, lit by level.
func bars(c *ui.Context, k theme.Colors, e proto.Effort) {
	lit := 2 // the default reads as medium
	switch e {
	case proto.EffortLow:
		lit = 1
	case proto.EffortHigh, proto.EffortXHigh:
		lit = 3
	case proto.EffortMax, proto.EffortUltra:
		lit = 3
	}
	ui.Row(c).Gap(2).AlignItems(ui.End).Label("强度 " + effortName(e)).Children(func() {
		for i := 1; i <= 3; i++ {
			color := k.Line2
			if i <= lit {
				color = k.Fg3
			}
			ui.Box(c.Key(i)).Size(3, float32(3+i*3)).Radius(1).Background(color)
		}
	})
}

// currentModel names the session's model as its harness lists it, and
// returns the entry when it is listed.
func currentModel(s proto.Session, h proto.HarnessInfo) (string, *proto.ModelInfo) {
	for i, m := range h.Models {
		if s.Model != "" && m.Matches(s.Model) {
			return m.Name, &h.Models[i]
		}
	}
	if s.Model != "" {
		return s.Model, nil
	}
	for i, m := range h.Models {
		if m.Default {
			return m.Name, &h.Models[i]
		}
	}
	return "默认模型", nil
}

// effortMenu is the effort and model popover: Fast, the current effort and
// model, reset, the models, the levels the model takes. Changes apply from
// the next message.
func (a *App) effortMenu(c *ui.Context, k theme.Colors, s proto.Session, h proto.HarnessInfo) {
	caps := h.Capabilities
	_, cur := currentModel(s, h)
	levels := caps.Efforts
	fast := caps.Fast
	if cur != nil && len(cur.Efforts) > 0 {
		levels = cur.Efforts
	}
	if cur != nil && h.Kind == "hermes" {
		fast = fast && cur.Fast
	}
	ui.Column(c).Width(300).Padding(8).Gap(6).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			on := s.Fast
			fb := iconButton(c, k, a.icons.Fast, "Fast：更快，更贵", on).Disabled(!fast)
			if fb.Clicked() {
				a.host.SetFast(proto.SetFastParams{Session: s.ID, Fast: !on})
			}
			model, _ := currentModel(s, h)
			theme.UI.On(ui.Text(c, "强度 "+effortName(s.Effort)+" · "+model)).TextColor(k.Fg).Grow(1).SingleLine()
			if iconButton(c, k, a.icons.Reset, "恢复默认", false).Clicked() {
				a.host.SetEffort(proto.SetEffortParams{Session: s.ID, Effort: ""})
			}
		})
		if len(levels) > 0 {
			labels := make([]string, len(levels))
			sel := -1
			for i, e := range levels {
				labels[i] = effortName(e)
				if e == s.Effort {
					sel = i
				}
			}
			if ui.Segmented(c, &sel, labels...).Label("强度").FillWidth().Changed() && sel >= 0 {
				a.host.SetEffort(proto.SetEffortParams{Session: s.ID, Effort: levels[sel]})
			}
		}
		if caps.SetModel && len(h.Models) > 0 {
			ui.Box(c).Height(1).FillWidth().Background(k.Line)
			ui.Scroll(c).MaxHeight(260).Children(func() {
				for i, m := range h.Models {
					r := menuRow(c.Key(i), k, cur != nil && cur.ID == m.ID && cur.Provider == m.Provider)
					if r.Clicked() {
						a.host.SetModel(proto.SetModelParams{Session: s.ID, Model: m.ID, Provider: m.Provider})
						a.s.effortOpen = false
					}
					r.Children(func() {
						ui.Column(c).Grow(1).Children(func() {
							theme.UI.On(ui.Text(c, m.Name)).TextColor(k.Fg).SingleLine()
							if m.Description != "" {
								theme.Caption.On(ui.Text(c, m.Description)).TextColor(k.Muted).SingleLine()
							}
						})
					})
				}
			})
		}
		theme.Caption.On(ui.Text(c, "下一条消息起生效")).TextColor(k.Faint)
	})
}

// queueTray shows the messages waiting for the turn to end, and those
// written while offline, above the composer.
func (a *App) queueTray(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo, steer bool) {
	queue := cv.Queue()
	outbox := a.outbox(s.ID)
	if len(queue) == 0 && len(outbox) == 0 {
		return
	}
	ui.Column(c).FillWidth().Margin(0, 12, 0, 12).Padding(6, 8).Gap(2).Radius(12, 12, 0, 0).Background(k.Glass).Border(1, k.Line).Children(func() {
		ui.Row(c).Height(24).Gap(4).AlignItems(ui.Center).Children(func() {
			theme.Label.On(ui.Textf(c, "排队 %d", len(queue)+len(outbox))).TextColor(k.Muted).Grow(1)
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
		shown := queue
		if len(shown) > 3 {
			shown = shown[:3]
		}
		for _, m := range shown {
			ui.Row(c.Key(m.Key)).Height(26).Gap(4).AlignItems(ui.Center).Children(func() {
				label := m.Text
				if m.Error != "" {
					label = m.Error + " · " + m.Text
				}
				theme.UI.On(ui.Text(c, label)).TextColor(k.Fg2).SingleLine().Grow(1).Shrink(1)
				if steer && iconButton(c, k, a.icons.Steer, "插话", false).Size(22, 22).Clicked() {
					a.host.Steer(proto.SteerParams{Session: s.ID, Text: m.Text})
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
				if iconButton(c, k, a.icons.Edit, "编辑", false).Size(22, 22).Clicked() {
					a.s.drafts[s.ID] = m.Text
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
				if iconButton(c, k, a.icons.Close, "删除", false).Size(22, 22).Clicked() {
					a.host.DeleteQueued(proto.QueueKeyParams{Session: s.ID, Key: m.Key})
				}
			})
		}
		if n := len(queue) - len(shown); n > 0 {
			theme.Caption.On(ui.Textf(c, "还有 %d 条", n)).TextColor(k.Muted)
		}
		for i, t := range outbox {
			ui.Row(c.Key(fmt.Sprintf("out:%d", i))).Height(26).Gap(6).AlignItems(ui.Center).Children(func() {
				theme.UI.On(ui.Text(c, t)).TextColor(k.Fg2).SingleLine().Grow(1).Shrink(1)
				theme.Caption.On(ui.Text(c, "重连后发送")).TextColor(k.Muted)
			})
		}
	})
}

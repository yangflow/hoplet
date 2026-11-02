package shell

import (
	"slices"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// popover shows a panel above anchor while *open, in the composer's look:
// a 10-point card with a hairline and the popover shadow, 6 points apart
// from the anchor. end aligns it with the anchor's end rather than its
// start.
func popover(c *ui.Context, k theme.Colors, anchor ui.Element, open *bool, end bool, width float32, fn func()) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		at, self := ui.AnchorTopStart, ui.AnchorBottomStart
		if end {
			at, self = ui.AnchorTopEnd, ui.AnchorBottomEnd
		}
		panel.AttachTo(anchor, at, self).Margin(0, 0, 6, 0).Width(width)
		frosted(c, k, panel, theme.RadiusCard, func(card ui.Element) { card.Padding(6).Gap(1) }, fn)
	})
}

// frostRadius is how much what lies under a popover blurs through it.
const frostRadius = 18

// frosted makes e a card of the popover look: what lies under it shows
// through blurred, under the popover fill, a hairline and the popover
// shadow. The fill is a card inside e, as an element takes a material or a
// fill, not both; style lays the card out and fn draws its content.
func frosted(c *ui.Context, k theme.Colors, e ui.Element, radius float32, style func(ui.Element), fn func()) {
	y, blur := float32(10), float32(30)
	if k.Dark {
		y, blur = 16, 40
	}
	e.Radius(radius).Material(glass.Blur{Radius: frostRadius}).Border(1, k.Line2).Shadow(0, y, blur, 0, k.Shadow).Children(func() {
		card := ui.Column(c).AlignSelf(ui.Stretch).Grow(1).Radius(radius).Background(k.Popover)
		style(card)
		card.Children(fn)
	})
}

// menuItem is a row of a popover: at least 28 high, a light background
// when on or hovered.
func menuItem(c *ui.Context, k theme.Colors, on bool) ui.Element {
	r := ui.Row(c).MinHeight(theme.RowHeight).Padding(4, 8).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusRow)
	switch {
	case on:
		r.Background(k.Sel)
	case r.Hovered():
		r.Background(k.Fill)
	}
	return r
}

// menuHeader names a group of a popover's rows.
func menuHeader(c *ui.Context, k theme.Colors, label string) {
	theme.Label.On(ui.Text(c, label)).TextColor(k.Muted).Padding(6, 8, 2, 8)
}

// menuSeparator is a line between groups of a popover.
func menuSeparator(c *ui.Context, k theme.Colors) {
	ui.Box(c).Height(1).FillWidth().Background(k.Line).Margin(4, 6)
}

// runSettings are what the composer's controls change: a session's, or
// the new-session page's before the session exists.
type runSettings struct {
	harness         string
	perm            proto.PermissionMode
	plan            bool
	effort          proto.Effort
	fast            bool
	model, provider string
}

// settingsSink applies changes to run settings.
type settingsSink struct {
	perm   func(proto.PermissionMode)
	plan   func(bool)
	effort func(proto.Effort)
	fast   func(bool)
	model  func(harness string, m proto.ModelInfo)
}

// harnessName is a harness's name as its maker writes it.
func harnessName(kind string) string {
	switch kind {
	case "claude":
		return "Claude Code"
	case "hermes":
		return "Hermes"
	case "codex":
		return "Codex"
	}
	return kind
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
		{proto.ModeConfirm, "编辑和命令都问你"},
		{proto.ModeAutoEdit, "编辑直接改，命令仍问"},
		{proto.ModeAutoReview, "harness 自己判断风险"},
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

// modeControl is the permission mode's shield and, while plan mode is on,
// the plan icon; the shield opens the modes the harness has and plan mode.
func (a *App) modeControl(c *ui.Context, k theme.Colors, st runSettings, caps proto.Capabilities, set settingsSink) {
	if !caps.SetMode {
		return
	}
	b := iconButton(c, k, a.modeIcon(st.perm), "审批："+modeName(st.perm), a.s.permOpen)
	if b.Clicked() {
		a.s.permOpen = !a.s.permOpen
	}
	popover(c, k, b, &a.s.permOpen, false, 260, func() {
		keys := []string{"⇧⌘1", "⇧⌘2", "⇧⌘3", "⇧⌘4"}
		for i, m := range permissionModes() {
			if !slices.Contains(caps.Modes, m.mode) {
				continue
			}
			on := st.perm == m.mode
			r := menuItem(c.Key(m.mode), k, on).Label(modeName(m.mode))
			if r.Clicked() {
				set.perm(m.mode)
				a.s.permOpen = false
			}
			r.Children(func() {
				ui.Icon(c, a.modeIcon(m.mode)).FontSize(theme.IconSize).TextColor(k.Fg3)
				ui.Column(c).Grow(1).Padding(4, 0).Children(func() {
					theme.UI.On(ui.Text(c, modeName(m.mode))).TextColor(k.Fg)
					theme.Caption.On(ui.Text(c, m.what)).TextColor(k.Muted)
				})
				theme.Caption.On(ui.Text(c, keys[i])).TextColor(k.Muted)
				check(c, k, a, on)
			})
		}
		if caps.PlanMode {
			menuSeparator(c, k)
			r := menuItem(c, k, false).Label("Plan")
			if r.Clicked() {
				set.plan(!st.plan)
			}
			r.Children(func() {
				ui.Icon(c, a.icons.Plan).FontSize(theme.IconSize).TextColor(k.Fg3)
				ui.Column(c).Grow(1).Padding(4, 0).Children(func() {
					theme.UI.On(ui.Text(c, "Plan")).TextColor(k.Fg)
					theme.Caption.On(ui.Text(c, "只想不改")).TextColor(k.Muted)
				})
				theme.Caption.On(ui.Text(c, "⇧⇥")).TextColor(k.Muted)
				check(c, k, a, st.plan)
			})
		}
	})
	if st.plan && iconButton(c, k, a.icons.Plan, "Plan ⇧⇥", true).Clicked() {
		set.plan(false)
	}
}

// currentModel finds the model st runs among h's: the one it names, else
// the harness's default. The name is what the pill shows.
func currentModel(st runSettings, h proto.HarnessInfo) (string, *proto.ModelInfo) {
	for i, m := range h.Models {
		if st.model != "" && m.Matches(st.model) {
			return m.Name, &h.Models[i]
		}
	}
	if st.model != "" {
		return st.model, nil
	}
	for i, m := range h.Models {
		if m.Default {
			return m.Name, &h.Models[i]
		}
	}
	return "默认模型", nil
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
	lit := 0 // the harness's own default: none lit
	switch e {
	case proto.EffortLow:
		lit = 1
	case proto.EffortMedium:
		lit = 2
	case proto.EffortHigh, proto.EffortXHigh, proto.EffortMax, proto.EffortUltra:
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

// check is the 14-point mark of a popover row's current choice; an empty
// space of its size otherwise.
func check(c *ui.Context, k theme.Colors, a *App, on bool) {
	if !on {
		ui.Box(c).Size(14, 14)
		return
	}
	ui.Icon(c, a.icons.Check).FontSize(14).TextColor(k.Fg3)
}

// effortPill shows the effort as bars, the harness's mark and the model;
// it opens the effort popover: Fast, the effort with the harness and the
// model, reset, and a slider over the levels the model takes. The model
// opens the model list (one harness for a session; every installed one
// on the new-session page, by harness).
func (a *App) effortPill(c *ui.Context, k theme.Colors, st runSettings, hs []proto.HarnessInfo, set settingsSink) {
	h := proto.HarnessInfo{Kind: st.harness}
	if i := slices.IndexFunc(hs, func(x proto.HarnessInfo) bool { return x.Kind == st.harness }); i >= 0 {
		h = hs[i]
	}
	// The level just chosen shows at once, before the daemon confirms
	// it, so the knob does not go back meanwhile.
	if !a.s.effortOpen || a.s.effortWant == st.effort {
		a.s.effortWanted = false
	}
	if a.s.effortWanted {
		st.effort = a.s.effortWant
	}
	setEffort := func(e proto.Effort) {
		a.s.effortWant, a.s.effortWanted = e, true
		set.effort(e)
	}
	model, cur := currentModel(st, h)
	b := ui.Button(c, "").Label("强度与模型").Tooltip(harnessName(h.Kind)+" · "+model+" · 推理强度 "+effortName(st.effort)).
		Height(theme.RowHeight).Padding(0, 8, 0, 9).Gap(6).Radius(theme.RadiusPill).Border(0, ui.Transparent).Background(k.Fill).
		Disabled(!a.connected) // which models there are is known only once connected
	if b.Clicked() {
		a.s.effortOpen = !a.s.effortOpen
		a.s.modelOpen = false
		if a.s.effortOpen && a.connected {
			a.host.Harnesses(a.SetHarnesses)
		}
	}
	b.Children(func() {
		bars(c, k, st.effort)
		a.harnessIcon(c, k, h.Kind, theme.IconSizeInRow)
		theme.Secondary.On(ui.Text(c, model)).TextColor(k.Fg3).SingleLine()
		ui.Icon(c, a.icons.Menu).FontSize(theme.IconSizeSmall).TextColor(k.Faint)
	})
	popover(c, k, b, &a.s.effortOpen, true, 300, func() {
		caps := h.Capabilities
		levels, fast := caps.Efforts, caps.Fast
		if cur != nil && len(cur.Efforts) > 0 {
			levels = cur.Efforts
		}
		if cur != nil && h.Kind == "hermes" {
			fast = fast && cur.Fast
		}
		if a.s.modelOpen {
			a.modelPage(c, k, st, cur, hs, set)
			return
		}
		ui.Column(c).Padding(6).Gap(12).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
				if iconButton(c, k, a.icons.Fast, "Fast：更快、更贵", st.fast).Disabled(!fast).Clicked() {
					set.fast(!st.fast)
				}
				ui.Column(c).Grow(1).AlignItems(ui.Center).Gap(2).Children(func() {
					a.effortLabel(c, k, st.effort)
					m := ui.Row(c).Gap(4).AlignItems(ui.Center).Label("换模型").Tooltip("换模型")
					if m.Clicked() {
						a.s.modelOpen, a.s.modelTab, a.s.modelQuery, a.s.modelPick = true, st.harness, "", -1
					}
					m.Children(func() {
						a.harnessIcon(c, k, h.Kind, theme.IconSizeSmall)
						theme.Secondary.On(ui.Text(c, model)).TextColor(k.Fg3).SingleLine()
						ui.Icon(c, a.icons.Expand).FontSize(10).TextColor(k.Faint)
					})
				})
				if iconButton(c, k, a.icons.Reset, "恢复默认", false).Disabled(st.effort == "").Clicked() {
					setEffort("")
				}
			})
			if len(levels) > 1 {
				a.effortSlider(c, k, levels, st.effort, setEffort)
			}
		})
	})
}

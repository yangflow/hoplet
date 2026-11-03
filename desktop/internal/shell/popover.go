package shell

import (
	"slices"

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
		panel.AttachTo(anchor, at, self).Margin(0, 0, 6, 0).Width(width).Padding(6).Gap(1).
			Radius(theme.RadiusCard).Background(k.Popover).Border(1, k.Line2).Shadow(0, 10, 30, 0, k.Shadow).Children(fn)
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
	fast, ultracode bool
	model, provider string
}

// settingsSink applies changes to run settings.
type settingsSink struct {
	perm      func(proto.PermissionMode)
	plan      func(bool)
	effort    func(proto.Effort)
	fast      func(bool)
	ultracode func(bool)
	model     func(harness string, m proto.ModelInfo)
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

// modeControl is the permission mode's button: its shield, its name and
// ⌄, opening the modes the harness has.
func (a *App) modeControl(c *ui.Context, k theme.Colors, st runSettings, caps proto.Capabilities, set settingsSink) {
	if !caps.SetMode {
		return
	}
	b := ui.Button(c, "").Label("审批："+modeName(st.perm)).Tooltip("审批："+modeName(st.perm)).
		Height(theme.RowHeight).Padding(0, 8).Gap(5).Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent)
	if a.s.permOpen || b.Hovered() {
		b.Background(k.Fill2)
	}
	if b.Clicked() {
		a.s.permOpen = !a.s.permOpen
	}
	b.Children(func() {
		ui.Icon(c, a.modeIcon(st.perm)).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
		theme.Secondary.On(ui.Text(c, modeName(st.perm))).TextColor(k.Fg2).SingleLine()
		ui.Icon(c, a.icons.Menu).FontSize(theme.IconSizeSmall).TextColor(k.Faint)
	})
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
	})
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

// effortName is an effort level as the harnesses name it; empty, the
// harness's own default, is 默认.
func effortName(e proto.Effort) string {
	switch e {
	case proto.EffortLow:
		return "Low"
	case proto.EffortMedium:
		return "Medium"
	case proto.EffortHigh:
		return "High"
	case proto.EffortXHigh:
		return "X-High"
	case proto.EffortMax:
		return "Max"
	case proto.EffortUltra:
		return "Ultra"
	}
	return "默认"
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

// effortLevels are the effort levels the model takes, or else its
// harness's.
func effortLevels(h proto.HarnessInfo, cur *proto.ModelInfo) []proto.Effort {
	switch {
	case cur != nil && cur.NoEffort:
		return nil
	case cur != nil && len(cur.Efforts) > 0:
		return cur.Efforts
	}
	return h.Capabilities.Efforts
}

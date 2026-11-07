package shell

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// modelRows is how many models the model submenu shows before it scrolls.
const modelRows = 8

// The run menu's submenus.
const (
	subEffort = "effort"
	subModel  = "model"
)

// runButton is the composer's model button: the harness's mark, the model,
// the effort and, in the accent, Plan, Fast and Ultracode while they are
// on. It opens the run menu: the model and the effort, each a submenu to
// the right, then switches for Fast, Plan and Ultracode where the harness
// and the model have them. The model submenu lists one harness's models
// for a session; on the new-session page, every installed harness's, one
// at a time, chosen in a column of their marks.
func (a *App) runButton(c *ui.Context, k theme.Colors, st runSettings, hs []proto.HarnessInfo, set settingsSink) {
	h := proto.HarnessInfo{Kind: st.harness}
	if i := slices.IndexFunc(hs, func(x proto.HarnessInfo) bool { return x.Kind == st.harness }); i >= 0 {
		h = hs[i]
	}
	model, cur := currentModel(st, h)
	levels := effortLevels(h, cur)
	fast := h.Capabilities.Fast && (cur == nil || cur.Fast)
	ultracode := h.Capabilities.Ultracode && slices.Contains(levels, proto.EffortXHigh)
	// A ghost button: no fill at rest, a light one under the pointer and
	// while its menu is up.
	b := ui.Button(c, "").Label("模型与强度").Tooltip(harnessName(h.Kind)+" · "+model+" · 推理强度 "+effortName(st.effort)).
		Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent).
		Disabled(!a.connected) // which models there are is known only once connected
	if a.s.runOpen || b.Hovered() {
		b.Background(k.Hover)
	}
	if b.Clicked() {
		a.s.runOpen, a.s.runSub = !a.s.runOpen, ""
		if a.s.runOpen && a.connected {
			a.host.Harnesses(a.SetHarnesses)
		}
	}
	b.Children(func() {
		a.harnessIcon(c, k, h.Kind, theme.IconRow)
		theme.Secondary.On(ui.Text(c, model)).TextColor(k.Fg2).SingleLine()
		if len(levels) > 0 {
			theme.Secondary.On(ui.Text(c, "·")).TextColor(k.Faint)
			theme.Secondary.On(ui.Text(c, effortName(st.effort))).TextColor(k.Fg3).SingleLine()
		}
		for _, on := range []struct {
			on   bool
			icon *ui.SVG
		}{{st.plan, a.icons.Plan}, {st.fast && fast, a.icons.Fast}, {st.ultracode && ultracode, a.icons.Ultracode}} {
			if on.on {
				ui.Icon(c, on.icon).FontSize(theme.UI.Size()).TextColor(k.AccentFg)
			}
		}
		ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
	})
	popover(c, k, b, &a.s.runOpen, false, 232, func() {
		if len(a.modelTabs(hs, st.harness)) > 0 {
			m := a.valueRow(c, k, "模型", subModel, func() {
				a.harnessIcon(c, k, h.Kind, theme.IconSmall)
				theme.Secondary.On(ui.Text(c, model)).TextColor(k.Muted).SingleLine().Shrink(1)
			})
			a.modelMenu(c, k, m, st, cur, hs, set)
		}
		if len(levels) > 0 { // a model without effort has no such row
			e := a.valueRow(c, k, "推理强度", subEffort, func() {
				theme.Secondary.On(ui.Text(c, effortName(st.effort))).TextColor(k.Muted).SingleLine()
			})
			a.effortMenu(c, k, e, st.effort, levels, set)
		}
		if !fast && !h.Capabilities.PlanMode && !ultracode {
			return
		}
		menuSeparator(c, k)
		if fast {
			a.switchRow(c, k, a.icons.Fast, "快速模式", st.fast, set.fast)
		}
		if h.Capabilities.PlanMode {
			a.switchRow(c, k, a.icons.Plan, "计划模式", st.plan, set.plan)
		}
		if ultracode {
			a.switchRow(c, k, a.icons.Ultracode, "Ultracode", st.ultracode, set.ultracode)
		}
	})
	if !a.s.runOpen {
		a.s.runSub, a.s.runHover = "", ""
	}
}

// valueRow is a row of the run menu opening a submenu: its name, then its
// value and › at the end. The pointer coming onto it, or a click, opens
// the submenu; one closed with ⎋ stays closed while the pointer rests.
func (a *App) valueRow(c *ui.Context, k theme.Colors, label, sub string, value func()) ui.Element {
	r := menuItem(c.Key(sub), k, a.s.runSub == sub).Label(label)
	switch {
	case r.Hovered() && a.s.runHover != sub:
		a.s.runSub, a.s.runHover = sub, sub
	case !r.Hovered() && a.s.runHover == sub:
		a.s.runHover = ""
	}
	if r.Clicked() {
		// The click took the focus; the model search gets it back.
		a.s.runSub, a.s.modelFocus = sub, sub == subModel
	}
	return r.Children(func() {
		// The name keeps its line; a long value gives way, ending in ….
		theme.UI.On(ui.Text(c, label)).TextColor(k.Fg).SingleLine().Shrink(0)
		ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(theme.Space1).AlignItems(ui.Center).Justify(ui.End).Children(value)
		ui.Icon(c, a.icons.Forward).FontSize(theme.IconSmall).TextColor(k.Faint)
	})
}

// switchRow is a row of the run menu turning a setting on and off: the
// whole row is the switch, and the menu stays up.
func (a *App) switchRow(c *ui.Context, k theme.Colors, icon *ui.SVG, label string, on bool, set func(bool)) {
	r := menuItem(c.Key(label), k, false).Label(label)
	if r.Hovered() {
		a.s.runSub = ""
	}
	r.Children(func() {
		ui.Icon(c, icon).FontSize(theme.Icon).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, label)).TextColor(k.Fg).Grow(1)
		v := on
		if ui.Switch(c, &v).Label(label).Changed() || r.Clicked() {
			set(!on)
		}
	})
}

// submenu shows a panel to the right of row while sub is the open one,
// lined up with its top, in the run menu's look.
func (a *App) submenu(c *ui.Context, k theme.Colors, row ui.Element, sub string, width float32, padded bool, fn func()) {
	open := a.s.runSub == sub
	ui.PopoverBase(c.Key("sub:"+sub), row, &open, func(panel ui.Element) {
		panel.AttachTo(row, ui.AnchorTopEnd, ui.AnchorTopStart).Margin(0, 0, 0, theme.Space2).Width(width).Gap(theme.SpaceHalf).
			Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Transition(popMotion)
		k.ShadowPopover.On(panel).Children(fn)
		if padded {
			panel.Padding(theme.Space1Half)
		}
	})
	if !open && a.s.runSub == sub {
		a.s.runSub = ""
	}
}

// effortMenu is the effort submenu: 默认, which leaves the effort to the
// harness, then the levels the model takes; the current one checked.
func (a *App) effortMenu(c *ui.Context, k theme.Colors, row ui.Element, cur proto.Effort, levels []proto.Effort, set settingsSink) {
	a.submenu(c, k, row, subEffort, 128, true, func() {
		for _, e := range append([]proto.Effort{""}, levels...) {
			r := menuItem(c.Key("effort:"+string(e)), k, false).Label(effortName(e))
			if r.Clicked() {
				set.effort(e)
			}
			r.Children(func() {
				theme.UI.On(ui.Text(c, effortName(e))).TextColor(k.Fg).Grow(1)
				check(c, k, a, e == cur)
			})
		}
	})
}

// modelTabs are the harnesses whose models the model submenu offers: the
// session's own when it can change models; on the new-session page, every
// installed one that listed models, and always the current one, which
// shows that it has none rather than another harness's.
func (a *App) modelTabs(hs []proto.HarnessInfo, current string) []proto.HarnessInfo {
	var tabs []proto.HarnessInfo
	for _, h := range hs {
		if (len(h.Models) > 0 || h.Kind == current) && (len(hs) > 1 || h.Capabilities.SetModel) {
			tabs = append(tabs, h)
		}
	}
	return tabs
}

// modelMenu is the model submenu: a search above the models, those of a
// provider under its name, the current one checked. With several
// harnesses, a column of their marks at the start picks whose models show.
func (a *App) modelMenu(c *ui.Context, k theme.Colors, row ui.Element, st runSettings, cur *proto.ModelInfo, hs []proto.HarnessInfo, set settingsSink) {
	tabs := a.modelTabs(hs, st.harness)
	var missing []proto.HarnessInfo // shown on the new-session page, to say how to install them
	if a.s.newPage {
		missing = a.s.missing
	}
	rail := len(tabs)+len(missing) > 1
	width := float32(280)
	if rail {
		width += 40
	}
	a.submenu(c, k, row, subModel, width, false, func() {
		all := append(slices.Clone(tabs), missing...)
		tab := slices.IndexFunc(all, func(h proto.HarnessInfo) bool { return h.Kind == a.s.modelTab })
		if tab < 0 {
			tab = max(slices.IndexFunc(all, func(h proto.HarnessInfo) bool { return h.Kind == st.harness }), 0)
		}
		ui.Row(c).AlignItems(ui.Stretch).Children(func() {
			if rail {
				ui.Column(c).Width(theme.PickerRail).Padding(theme.Space1Half, 0).Gap(theme.Space1).AlignItems(ui.Center).BorderWidth(0, theme.LineWidth, 0, 0).BorderColor(k.Line).Children(func() {
					for i, h := range all {
						tip := harnessName(h.Kind)
						if h.Missing != "" {
							tip += " · 未安装"
						}
						b := ui.Button(c.Key("tab:"+h.Kind), "").Label(harnessName(h.Kind)).Tooltip(tip).
							Size(theme.Row, theme.Row).Padding(0).Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent)
						if i == tab || b.Hovered() {
							b.Background(k.Fill)
						}
						if b.Clicked() && i != tab {
							a.s.modelTab, a.s.modelPick = h.Kind, -1
						}
						b.Children(func() {
							if h.Missing != "" {
								ui.Box(c).Opacity(theme.OpacityFaded).Children(func() { a.harnessIcon(c, k, h.Kind, theme.Icon) })
								return
							}
							a.harnessIcon(c, k, h.Kind, theme.Icon)
						})
					}
				})
			}
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Padding(theme.Space1Half).Gap(theme.SpaceHalf).Children(func() {
				if h := all[tab]; h.Missing != "" {
					a.missingHarness(c, k, h)
				} else {
					a.modelList(c, k, h, st, cur, set)
				}
			})
		})
	})
}

// modelList is the search and the models of h, for the model submenu.
func (a *App) modelList(c *ui.Context, k theme.Colors, h proto.HarnessInfo, st runSettings, cur *proto.ModelInfo, set settingsSink) {
	q := strings.ToLower(strings.TrimSpace(a.s.modelQuery))
	var models []proto.ModelInfo
	for _, m := range h.Models {
		if q == "" || strings.Contains(strings.ToLower(m.Name), q) {
			models = append(models, m)
		}
	}
	current := func(m proto.ModelInfo) bool {
		return h.Kind == st.harness && cur != nil && cur.ID == m.ID && cur.Provider == m.Provider
	}
	pick := a.s.modelPick
	if pick < 0 || pick >= len(models) {
		pick = max(slices.IndexFunc(models, current), 0)
	}
	choose := func(m proto.ModelInfo) {
		set.model(h.Kind, m)
		a.s.modelPick = -1
	}
	ui.Box(c).Margin(0, 0, theme.SpaceHalf, 0).Children(func() {
		in := a.searchField(c, k, &a.s.modelQuery, "搜索模型", func(key ui.TextKey) bool {
			switch {
			case key.Mods != 0:
				return false
			case key.Key == ui.KeyUp:
				a.s.modelPick = max(pick-1, 0)
			case key.Key == ui.KeyDown:
				a.s.modelPick = min(pick+1, len(models)-1)
			case key.Key == ui.KeyEnter:
				if pick < len(models) {
					choose(models[pick])
				}
			case key.Key == ui.KeyEscape, key.Key == ui.KeyLeft && a.s.modelQuery == "":
				a.s.runSub = ""
			default:
				return false
			}
			return true
		}).AutoFocus()
		if a.s.modelFocus {
			in.Focus()
			a.s.modelFocus = false
		}
	})
	switch {
	case len(h.Models) == 0:
		ui.Text(c, harnessName(h.Kind)+" 还没有给出模型列表").FontSize(theme.CodeInline.Size()).TextColor(k.Muted).Padding(theme.Space1Half, theme.Space2, theme.Space2, theme.Space2)
		return
	case len(models) == 0:
		ui.Text(c, "没有匹配“"+a.s.modelQuery+"”的模型").FontSize(theme.CodeInline.Size()).TextColor(k.Muted).Padding(theme.Space1Half, theme.Space2, theme.Space2, theme.Space2)
		return
	}
	ui.Scroll(c.Key(h.Kind)).MaxHeight(modelRows * theme.Row).Children(func() {
		for i, m := range models {
			// Models of a provider go under its name.
			if m.ProviderName != "" && (i == 0 || models[i-1].ProviderName != m.ProviderName) {
				menuHeader(c.Key("provider:"+m.Provider), k, m.ProviderName)
			}
			r := menuItem(c.Key(i), k, i == pick).Height(theme.Row).Padding(0, theme.Space2).Label(m.Name).Tooltip(m.Description)
			if r.Clicked() {
				choose(m)
			}
			r.Children(func() {
				a.harnessIcon(c, k, h.Kind, theme.IconRow)
				theme.UI.On(ui.Text(c, m.Name)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1)
				if m.Default && m.ID != "default" { // Claude names its default entry itself
					theme.Caption.On(ui.Text(c, "默认")).TextColor(k.Muted)
				}
				check(c, k, a, current(m))
			})
		}
	})
}

// missingHarness says, in the model submenu, that a harness cannot run
// and how to install it; why it cannot is under the pointer.
func (a *App) missingHarness(c *ui.Context, k theme.Colors, h proto.HarnessInfo) {
	ui.Column(c).Padding(theme.Space1Half, theme.Space2, theme.Space2, theme.Space2).Gap(theme.Space1).Tooltip(h.Missing).Children(func() {
		theme.UIStrong.On(ui.Text(c, harnessName(h.Kind)+" 未安装")).TextColor(k.Fg)
		switch h.Kind {
		case "claude":
			ui.Text(c, "在终端运行 hoplet harness install claude").FontSize(theme.CodeInline.Size()).TextColor(k.Muted)
		case "hermes":
			ui.Text(c, "安装 Hermes Agent 后，再运行 hoplet harness verify hermes").FontSize(theme.CodeInline.Size()).TextColor(k.Muted)
		}
	})
}

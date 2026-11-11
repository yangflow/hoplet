package shell

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// memorySettingsWidth is the settings panel's width: half again the details
// column.
const memorySettingsWidth = theme.DetailsWidth * 3 / 2

// memorySettingsView is the settings panel over the page.
type memorySettingsView struct {
	open    bool
	pick    bool // the menu of what extraction uses shows
	writing int  // counts the writes: only the latest answer counts
}

// memoryBarEnd is the title bar's right end: the settings button, and
// beside it the mark that extraction has failed. Until the settings have
// been read there is nothing to change.
func (a *App) memoryBarEnd(c *ui.Context, k theme.Colors) {
	if !a.m.known || a.m.err != nil {
		return
	}
	ui.Box(c).Grow(1)
	ui.Row(c).Padding(0, theme.Space1, 0, 0).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		if a.m.data.Extraction.Failed > 0 {
			memoryErrorDot(c, k)
		}
		v := &a.m.settings
		b := iconButton(c, k, a.icons.Settings, "记忆设置", v.open)
		if b.Clicked() {
			v.open, v.pick = !v.open, false
		}
		ui.PopoverBase(c, b, &v.open, func(panel ui.Element) {
			panel.AttachTo(b, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(theme.Space2, 0, 0, 0).Width(memorySettingsWidth).Padding(theme.Space1).Gap(theme.SpaceHalf).
				Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Label("记忆设置").Transition(popMotion)
			k.ShadowPopover.On(panel).Children(func() { a.memorySettingsRows(c, k) })
		})
	})
}

// memoryErrorDot is the status mark of a failure.
func memoryErrorDot(c *ui.Context, k theme.Colors) {
	ui.Box(c).Size(theme.Dot, theme.Dot).Radius(theme.RadiusMark).Background(k.Fg).Label("出错")
}

// setMemorySettings changes the settings here at once, and goes back with
// a note if the daemon does not take them.
func (a *App) setMemorySettings(next proto.ProfileSettings) {
	prev := a.m.data.Settings
	a.m.data.Settings = next
	a.m.settings.writing++
	n := a.m.settings.writing
	a.host.SetMemorySettings(next, func(s proto.ProfileSettings, err error) {
		if n != a.m.settings.writing {
			return
		}
		if err != nil {
			a.m.data.Settings = prev
			a.hint("设置没有保存")
			return
		}
		a.m.data.Settings = s
	})
}

// memorySettingsRows are the panel's two groups.
func (a *App) memorySettingsRows(c *ui.Context, k theme.Colors) {
	s := a.m.data.Settings
	set := func(change func(*proto.ProfileSettings)) func() {
		return func() {
			next := s
			change(&next)
			a.setMemorySettings(next)
		}
	}
	memorySettingsHead(c, k, "使用", 0)
	memorySwitchRow(c, k, "Personal 会话", s.UsePersonal, nil, set(func(n *proto.ProfileSettings) { n.UsePersonal = !s.UsePersonal }))
	memorySwitchRow(c, k, "Code 会话", s.UseCode, nil, set(func(n *proto.ProfileSettings) { n.UseCode = !s.UseCode }))
	memorySettingsHead(c, k, "提炼", theme.Space1)
	memorySwitchRow(c, k, "后台提炼", s.Extraction, func() {
		if n := a.m.data.Extraction.Failed; n > 0 {
			ui.Row(c).Gap(theme.Space1Half).AlignItems(ui.Center).Shrink(0).Children(func() {
				memoryErrorDot(c, k)
				theme.Caption.On(ui.Text(c, strconv.Itoa(n)+" 次失败")).TextColor(k.Muted)
			})
		}
	}, set(func(n *proto.ProfileSettings) { n.Extraction = !s.Extraction }))
	a.memoryPickRow(c, k, s)
	memorySwitchRow(c, k, "包含 Code 会话", s.IncludeCode, nil, set(func(n *proto.ProfileSettings) { n.IncludeCode = !s.IncludeCode }))
	memorySwitchRow(c, k, "自动保存", s.AutoSave, func() {
		theme.Caption.On(ui.Text(c, "关闭后全部待确认")).TextColor(k.Muted).Shrink(0)
	}, set(func(n *proto.ProfileSettings) { n.AutoSave = !s.AutoSave }))
}

// memorySettingsHead names a group of the panel.
func memorySettingsHead(c *ui.Context, k theme.Colors, name string, above float32) {
	ui.Row(c).Height(theme.RowDense).Margin(above, 0, 0, 0).Padding(0, theme.Space2).AlignItems(ui.Center).Children(func() {
		theme.Label.On(ui.Text(c, name)).TextColor(k.Muted)
	})
}

// memorySwitchRow is a setting: its name, what extra says about it, and its
// switch.
func memorySwitchRow(c *ui.Context, k theme.Colors, name string, on bool, extra, toggle func()) {
	ui.Row(c.Key(name)).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		theme.UI.On(ui.Text(c, name)).TextColor(k.Fg).Grow(1).Shrink(1).SingleLine()
		if extra != nil {
			extra()
		}
		v := on
		if ui.Switch(c, &v).Label(name).Changed() {
			toggle()
		}
	})
}

// memoryPickRow chooses the harness extraction uses.
func (a *App) memoryPickRow(c *ui.Context, k theme.Colors, s proto.ProfileSettings) {
	v := &a.m.settings
	ui.Row(c.Key("pick")).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		theme.UI.On(ui.Text(c, "提炼使用")).TextColor(k.Fg).Grow(1).Shrink(1).SingleLine()
		value := "跟随会话"
		if s.ExtractionHarness != "" {
			value = harnessName(s.ExtractionHarness)
		}
		b := ui.Row(c.Key("value")).Gap(theme.Space1).AlignItems(ui.Center).Label("提炼使用：" + value)
		if b.Clicked() {
			v.pick = !v.pick
		}
		b.Children(func() {
			if s.ExtractionHarness == "" {
				theme.Caption.On(ui.Text(c, value)).TextColor(k.Fg3)
			} else {
				a.harnessIcon(c, k, s.ExtractionHarness, theme.IconSmall)
				theme.Caption.On(ui.Text(c, value)).TextColor(k.Fg2)
			}
			ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
		})
		ui.PopoverBase(c, b, &v.pick, func(panel ui.Element) {
			panel.AttachTo(b, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(theme.Space1, 0, 0, 0).Width(theme.MenuWidth).Padding(theme.Space1).Gap(theme.SpaceHalf).
				Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Transition(popMotion)
			k.ShadowPopover.On(panel).Children(func() { a.memoryPickMenu(c, k, s) })
		})
	})
}

// memoryPickMenu lists following the session, then the harnesses: those
// that can extract, then those that cannot, greyed.
func (a *App) memoryPickMenu(c *ui.Context, k theme.Colors, s proto.ProfileSettings) {
	choose := func(kind string) {
		a.m.settings.pick = false
		if kind != s.ExtractionHarness {
			next := s
			next.ExtractionHarness = kind
			a.setMemorySettings(next)
		}
	}
	follow := s.ExtractionHarness == ""
	r := menuItem(c.Key("follow"), k, follow).Label("跟随会话")
	if r.Clicked() {
		choose("")
	}
	r.Children(func() {
		theme.UI.On(ui.Text(c, "跟随会话")).TextColor(k.Fg).Grow(1)
		check(c, k, a, follow)
	})
	hs := slices.Clone(a.m.data.Extraction.Harnesses)
	slices.SortStableFunc(hs, func(x, y proto.ProfileExtractionHarness) int {
		return cmp.Compare(unsupportedRank(x), unsupportedRank(y))
	})
	for _, h := range hs {
		on := h.Kind == s.ExtractionHarness
		supported := h.Unsupported == ""
		row := menuItem(c.Key(h.Kind), k, on && supported).Label(harnessName(h.Kind))
		if !supported {
			row.Opacity(theme.OpacityDisabled)
		} else if row.Clicked() {
			choose(h.Kind)
		}
		row.Children(func() {
			a.harnessIcon(c, k, h.Kind, theme.Icon)
			theme.UI.On(ui.Text(c, harnessName(h.Kind))).TextColor(k.Fg).Grow(1).Shrink(1).SingleLine()
			if !supported {
				theme.Caption.On(ui.Text(c, "不支持")).TextColor(k.Muted).Shrink(0)
			}
			check(c, k, a, on && supported)
		})
	}
	ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space2).AlignItems(ui.Center).Children(func() {
		theme.Caption.On(ui.Text(c, "指定后，所有会话的材料都发给它")).TextColor(k.Muted)
	})
}

func unsupportedRank(h proto.ProfileExtractionHarness) int {
	if h.Unsupported != "" {
		return 1
	}
	return 0
}

package shell

import (
	"testing"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

func TestFit(t *testing.T) {
	side := fitIn{sideOpen: true, side: theme.SidebarWidth, sideMin: theme.SidebarMin, rail: theme.PanelRail}
	with := func(w float32, f func(*fitIn)) fitIn {
		in := side
		in.width = w
		if f != nil {
			f(&in)
		}
		return in
	}
	panel := func(in *fitIn) { in.panelOpen, in.panelAt = true, 1 }
	viewer := func(in *fitIn) { panel(in); in.viewer = true }
	for _, tc := range []struct {
		name string
		in   fitIn
		want fitOut
	}{
		{"sidebar alone", with(1440, nil), fitOut{side: 260}},
		{"sidebar and column", with(1440, panel), fitOut{side: 260, panel: 260, rail: 260}},
		{"viewer takes half, at least its least and the column", with(1440, viewer), fitOut{side: 260, panel: 620, rail: 260}},
		{"viewer takes half", with(1800, viewer), fitOut{side: 260, panel: 770, rail: 260}},
		{"dragged viewer", with(1800, func(in *fitIn) { viewer(in); in.panel = 700 }), fitOut{side: 260, panel: 700, rail: 260}},
		{"sidebar squeezes first", with(1060, panel), fitOut{side: 240, panel: 260, rail: 260}},
		{"then the column", with(1000, panel), fitOut{side: 220, panel: 220, rail: 220}},
		{"then the viewer", with(1360, viewer), fitOut{side: 220, panel: 580, rail: 220}},
		{"the sidebar, opened first, yields", with(999, panel), fitOut{panel: 260, rail: 260}},
		{"the panel, opened first, yields", with(999, func(in *fitIn) { panel(in); in.sideAt = 2 }), fitOut{side: 260}},
		{"too narrow for any", with(700, panel), fitOut{}},
		{"closed sidebar", with(900, func(in *fitIn) { panel(in); in.sideOpen = false }), fitOut{panel: 260, rail: 260}},
	} {
		if got := fit(tc.in); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

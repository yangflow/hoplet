package shell

import "github.com/yangflow/hoplet/desktop/internal/theme"

// fitIn is what the window's columns ask for: whether each is open, the
// width it was given and when it was last opened. A column opened later
// holds its place longer.
type fitIn struct {
	width float32 // the window's

	sideOpen bool
	side     float32 // the sidebar's width, as dragged
	sideMin  float32 // the least it narrows to
	sideAt   int     // 0 unless opened by me in this run

	panelOpen bool // the right panel, for a session that has one
	viewer    bool // a tab is open: the viewer shows beside the column
	rail      float32
	panel     float32 // the panel's width with the viewer, as dragged; 0 shares the room evenly
	panelAt   int
}

// fitOut is how the columns show: the widths they take, 0 for one hidden.
// rail is the column's part of the panel.
type fitOut struct {
	side, panel, rail float32
}

// fit lays out the window's columns, leaving the content area at least
// content-min. Short of room, the sidebar, then the panel narrow to their
// least; still short, the column opened earliest hides, then the next.
// Hiding changes no column's open flag: a wider window brings it back.
func fit(in fitIn) fitOut {
	side, panel := in.sideOpen, in.panelOpen
	for {
		out := fitOut{}
		if side {
			out.side = in.side
		}
		if panel {
			out.panel, out.rail = in.rail, in.rail
			if in.viewer {
				out.panel = in.panel
				if out.panel == 0 {
					out.panel = (in.width - out.side) / 2
				}
				out.panel = max(out.panel, theme.ViewerMin+in.rail)
			}
		}
		over := out.side + out.panel + theme.ContentMin - in.width
		if side && over > 0 {
			d := min(over, out.side-in.sideMin)
			out.side -= d
			over -= d
		}
		if panel && over > 0 {
			least := float32(theme.PanelRailMin)
			if in.viewer {
				least += theme.ViewerMin
			}
			d := min(over, out.panel-least)
			out.panel -= d
			over -= d
		}
		if over <= 0 {
			if panel && in.viewer {
				out.rail = min(in.rail, out.panel-theme.ViewerMin)
			} else if panel {
				out.rail = out.panel
			}
			return out
		}
		switch {
		case side && panel && in.sideAt <= in.panelAt, side && !panel:
			side = false
		default:
			panel = false
		}
		if !side && !panel {
			return fitOut{}
		}
	}
}

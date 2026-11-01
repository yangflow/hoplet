// Package icons embeds the desktop's vendored icons: a subset of Lucide
// (ISC), the Claude mark from Simple Icons (CC0) and Hoplet's own mark.
// Licenses sit beside the files.
package icons

import (
	"embed"
	"fmt"

	"github.com/egoist/mygo/ui"
)

//go:embed lucide/*.svg simpleicons/*.svg brand/*.svg tray/*.png
var files embed.FS

// Set holds the parsed icons. Build it once with Load and share it.
type Set struct {
	Sidebar   *ui.SVG // collapse or show the sidebar
	Back      *ui.SVG
	Forward   *ui.SVG
	Personal  *ui.SVG // Personal mode
	Code      *ui.SVG // Code mode
	Folder    *ui.SVG // working directory group
	Pin       *ui.SVG
	Unpin     *ui.SVG
	Archive   *ui.SVG
	Arrange   *ui.SVG // sort and filter menu
	Machine   *ui.SVG // this machine
	Connected *ui.SVG // the daemon is reachable
	Offline   *ui.SVG // the daemon is not reachable
	Menu      *ui.SVG // a session's menu
	Claude    *ui.SVG // the Claude Code harness
	Mark      *ui.SVG // Hoplet's mark
}

// Load parses every icon.
func Load() (*Set, error) {
	s := &Set{}
	for _, f := range []struct {
		dst  **ui.SVG
		path string
	}{
		{&s.Sidebar, "lucide/panel-left.svg"},
		{&s.Back, "lucide/chevron-left.svg"},
		{&s.Forward, "lucide/chevron-right.svg"},
		{&s.Personal, "lucide/message-circle.svg"},
		{&s.Code, "lucide/code-xml.svg"},
		{&s.Folder, "lucide/folder.svg"},
		{&s.Pin, "lucide/pin.svg"},
		{&s.Unpin, "lucide/pin-off.svg"},
		{&s.Archive, "lucide/archive.svg"},
		{&s.Arrange, "lucide/list-filter.svg"},
		{&s.Machine, "lucide/monitor.svg"},
		{&s.Connected, "lucide/link.svg"},
		{&s.Offline, "lucide/unlink.svg"},
		{&s.Menu, "lucide/chevron-down.svg"},
		{&s.Claude, "simpleicons/claude.svg"},
		{&s.Mark, "brand/mark.svg"},
	} {
		data, err := files.ReadFile(f.path)
		if err != nil {
			return nil, fmt.Errorf("icons: %w", err)
		}
		if *f.dst, err = ui.ParseSVG(data); err != nil {
			return nil, fmt.Errorf("icons: parse %s: %w", f.path, err)
		}
	}
	return s, nil
}

// TrayState is what the menu bar icon shows through its hopping square.
type TrayState int

// Tray states.
const (
	TrayIdle    TrayState = iota // the square at 42%
	TrayRunning                  // a session is running: the square solid
	TrayOffline                  // the daemon is not reachable: the square nearly gone
)

// trayFile names the rendered template image of a state: 18 points at 2x.
func trayFile(s TrayState) string {
	switch s {
	case TrayRunning:
		return "tray/running.png"
	case TrayOffline:
		return "tray/offline.png"
	}
	return "tray/idle.png"
}

// Tray returns the menu bar template image (PNG) for a state.
func Tray(s TrayState) []byte {
	// The files are embedded and TestRenderedImages keeps them present, so
	// reading cannot fail.
	data, _ := files.ReadFile(trayFile(s))
	return data
}

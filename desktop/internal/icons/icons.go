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

	// The conversation and the composer.
	New      *ui.SVG // new session
	Copy     *ui.SVG
	Check    *ui.SVG
	Read     *ui.SVG // tool: read
	Search   *ui.SVG // tool: search
	Edit     *ui.SVG // tool: edit
	Write    *ui.SVG // tool: write a new file
	Run      *ui.SVG // tool: run a command
	Web      *ui.SVG // tool: fetch a web page
	Tool     *ui.SVG // tool: MCP and others
	Task     *ui.SVG // tool: a subtask
	Thinking *ui.SVG
	Allowed  *ui.SVG // a decided approval: allowed
	Denied   *ui.SVG // a decided approval: denied
	Confirm  *ui.SVG // permission: confirm
	AutoEdit *ui.SVG // permission: auto_edit
	Review   *ui.SVG // permission: auto_review
	FullAuto *ui.SVG // permission: full_auto
	Plan     *ui.SVG
	Steer    *ui.SVG
	Warning  *ui.SVG
	Reset    *ui.SVG
	ToBottom *ui.SVG
	Send     *ui.SVG
	Stop     *ui.SVG
	Expand   *ui.SVG // a folded group or row
	Fast     *ui.SVG
	Close    *ui.SVG
	Me       *ui.SVG // my avatar
	Info     *ui.SVG // session details
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
		{&s.New, "lucide/plus.svg"},
		{&s.Copy, "lucide/copy.svg"},
		{&s.Check, "lucide/check.svg"},
		{&s.Read, "lucide/file-text.svg"},
		{&s.Search, "lucide/search.svg"},
		{&s.Edit, "lucide/pencil.svg"},
		{&s.Write, "lucide/file-plus.svg"},
		{&s.Run, "lucide/terminal.svg"},
		{&s.Web, "lucide/globe.svg"},
		{&s.Tool, "lucide/box.svg"},
		{&s.Task, "lucide/git-branch.svg"},
		{&s.Thinking, "lucide/message-square-text.svg"},
		{&s.Allowed, "lucide/shield-check.svg"},
		{&s.Denied, "lucide/shield-x.svg"},
		{&s.Confirm, "lucide/shield-question-mark.svg"},
		{&s.AutoEdit, "lucide/shield-half.svg"},
		{&s.Review, "lucide/shield-check.svg"},
		{&s.FullAuto, "lucide/shield-off.svg"},
		{&s.Plan, "lucide/list-checks.svg"},
		{&s.Steer, "lucide/corner-down-right.svg"},
		{&s.Warning, "lucide/triangle-alert.svg"},
		{&s.Reset, "lucide/rotate-ccw.svg"},
		{&s.ToBottom, "lucide/arrow-down.svg"},
		{&s.Send, "lucide/arrow-up.svg"},
		{&s.Stop, "lucide/square.svg"},
		{&s.Expand, "lucide/chevron-right.svg"},
		{&s.Fast, "lucide/zap.svg"},
		{&s.Close, "lucide/x.svg"},
		{&s.Me, "lucide/user.svg"},
		{&s.Info, "lucide/info.svg"},
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

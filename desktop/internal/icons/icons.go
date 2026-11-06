// Package icons embeds the desktop's vendored icons: a subset of Lucide
// (ISC), file-type icons from Material Icon Theme (MIT), the harness marks
// (MIT) and Hoplet's own mark. Licenses sit beside the files.
package icons

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed lucide/*.svg material/*.svg harness/*.svg brand/*.svg tray/*.png
var files embed.FS

// Set holds the parsed icons. Build it once with Load and share it.
type Set struct {
	files     map[string]*ui.SVG // file-type icons, by Material Icon Theme name
	Sidebar   *ui.SVG            // collapse or show the sidebar
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
	Codex     *ui.SVG // the Codex harness
	Hermes    *ui.SVG // the Hermes harness
	Mark      *ui.SVG // Hoplet's mark

	// The conversation and the composer.
	New       *ui.SVG // new session
	Copy      *ui.SVG
	Check     *ui.SVG
	Read      *ui.SVG // tool: read
	Search    *ui.SVG // tool: search
	Edit      *ui.SVG // tool: edit
	Write     *ui.SVG // tool: write a new file
	Run       *ui.SVG // tool: run a command
	Web       *ui.SVG // tool: fetch a web page
	Tool      *ui.SVG // tool: MCP and others
	Task      *ui.SVG // tool: a subtask
	Thinking  *ui.SVG
	Allowed   *ui.SVG // a decided approval: allowed
	Denied    *ui.SVG // a decided approval: denied
	Confirm   *ui.SVG // permission: confirm
	AutoEdit  *ui.SVG // permission: auto_edit
	Review    *ui.SVG // permission: auto_review
	FullAuto  *ui.SVG // permission: full_auto
	Plan      *ui.SVG
	Steer     *ui.SVG
	Warning   *ui.SVG
	ToBottom  *ui.SVG
	Older     *ui.SVG // load a conversation's earlier messages
	Send      *ui.SVG
	Stop      *ui.SVG
	Expand    *ui.SVG // a folded group or row
	Note      *ui.SVG // text written between a turn's steps
	Fast      *ui.SVG
	Ultracode *ui.SVG // Ultracode: workflows of agents
	Close     *ui.SVG
	Me        *ui.SVG // my avatar
	Details   *ui.SVG // open or close the session details column (ⓘ)
	ID        *ui.SVG // a session's ID
	Made      *ui.SVG // a session's state and when it was made
	Grip      *ui.SVG // drag handle
	Repos     *ui.SVG // the repositories in a directory
	Reconnect *ui.SVG
	Fold      *ui.SVG // fold an opened output
	Unfold    *ui.SVG // show all of an output
	Failed    *ui.SVG // a toast saying something did not happen
	Inbox     *ui.SVG // the inbox
	Open      *ui.SVG // open a session from elsewhere, as an inbox card

	// The right panel.
	Panel      *ui.SVG // show or hide the right panel
	Tree       *ui.SVG // the file tree column
	Changes    *ui.SVG // the changes column and tab
	Maximize   *ui.SVG // the viewer over the whole content area
	SideBySide *ui.SVG // a diff's old and new side by side
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
		{&s.Claude, "harness/claude-mark.svg"},
		{&s.Codex, "harness/openai-mark.svg"},
		{&s.Hermes, "harness/hermes-mark.svg"},
		{&s.Mark, "brand/mark.svg"},
		{&s.New, "lucide/plus.svg"},
		{&s.Copy, "lucide/copy.svg"},
		{&s.Check, "lucide/check.svg"},
		{&s.Read, "lucide/file-text.svg"},
		{&s.Search, "lucide/search.svg"},
		{&s.Edit, "lucide/pencil.svg"},
		{&s.Write, "lucide/file-plus.svg"},
		{&s.Run, "lucide/square-terminal.svg"},
		{&s.Web, "lucide/globe.svg"},
		{&s.Tool, "lucide/box.svg"},
		{&s.Task, "lucide/git-branch.svg"},
		{&s.Thinking, "lucide/brain.svg"},
		{&s.Allowed, "lucide/shield-check.svg"},
		{&s.Denied, "lucide/shield-x.svg"},
		{&s.Confirm, "lucide/shield-question-mark.svg"},
		{&s.AutoEdit, "lucide/shield-half.svg"},
		{&s.Review, "lucide/shield-check.svg"},
		{&s.FullAuto, "lucide/shield-off.svg"},
		{&s.Plan, "lucide/list-checks.svg"},
		{&s.Steer, "lucide/corner-down-right.svg"},
		{&s.Warning, "lucide/triangle-alert.svg"},
		{&s.ToBottom, "lucide/arrow-down.svg"},
		{&s.Older, "lucide/arrow-up.svg"},
		{&s.Send, "lucide/arrow-up.svg"},
		{&s.Stop, "lucide/square.svg"},
		{&s.Expand, "lucide/chevron-right.svg"},
		{&s.Note, "lucide/message-circle.svg"},
		{&s.Fast, "lucide/zap.svg"},
		{&s.Ultracode, "lucide/workflow.svg"},
		{&s.Close, "lucide/x.svg"},
		{&s.Me, "lucide/user.svg"},
		{&s.Details, "lucide/info.svg"},
		{&s.ID, "lucide/id-card.svg"},
		{&s.Made, "lucide/clock.svg"},
		{&s.Grip, "lucide/grip-vertical.svg"},
		{&s.Repos, "lucide/folder-git-2.svg"},
		{&s.Reconnect, "lucide/refresh-cw.svg"},
		{&s.Fold, "lucide/chevrons-down-up.svg"},
		{&s.Unfold, "lucide/chevrons-up-down.svg"},
		{&s.Failed, "lucide/circle-alert.svg"},
		{&s.Inbox, "lucide/inbox.svg"},
		{&s.Open, "lucide/external-link.svg"},
		{&s.Panel, "lucide/panel-right.svg"},
		{&s.Tree, "lucide/folder-tree.svg"},
		{&s.Changes, "lucide/diff.svg"},
		{&s.Maximize, "lucide/maximize-2.svg"},
		{&s.SideBySide, "lucide/columns-2.svg"},
	} {
		data, err := files.ReadFile(f.path)
		if err != nil {
			return nil, fmt.Errorf("icons: %w", err)
		}
		if *f.dst, err = ui.ParseSVG(data); err != nil {
			return nil, fmt.Errorf("icons: parse %s: %w", f.path, err)
		}
	}
	names, err := fs.Glob(files, "material/*.svg")
	if err != nil {
		return nil, fmt.Errorf("icons: %w", err)
	}
	s.files = make(map[string]*ui.SVG, len(names))
	for _, n := range names {
		data, err := files.ReadFile(n)
		if err != nil {
			return nil, fmt.Errorf("icons: %w", err)
		}
		if s.files[strings.TrimSuffix(path.Base(n), ".svg")], err = ui.ParseSVG(data); err != nil {
			return nil, fmt.Errorf("icons: parse %s: %w", n, err)
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

// fileKind is the Material Icon Theme name of a file's icon, by its whole
// lowercased name, then its extension, else a plain document.
func fileKind(base string) string {
	switch base {
	case "go.mod", "go.sum":
		return "go-mod"
	case "makefile":
		return "makefile"
	case "dockerfile":
		return "docker"
	case ".gitignore", ".gitattributes", ".gitmodules":
		return "git"
	}
	switch ext := path.Ext(base); ext {
	case ".go", ".swift", ".json", ".toml", ".html", ".css", ".xml", ".lock", ".java", ".php", ".vue", ".svelte", ".proto":
		return ext[1:]
	case ".ts":
		return "typescript"
	case ".tsx":
		return "react_ts"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "react"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".md":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".sh", ".bash", ".zsh":
		return "console"
	case ".scss", ".sass":
		return "sass"
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp":
		return "image"
	case ".sql", ".db":
		return "database"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".hpp":
		return "cpp"
	case ".kt":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".ini", ".conf", ".cfg":
		return "settings"
	}
	return "document"
}

// FileType returns the icon of a file by its name, in its own colors.
func (s *Set) FileType(name string) *ui.SVG {
	return s.files[fileKind(strings.ToLower(path.Base(name)))]
}

// Harness is the mark of a harness kind, nil for one without a mark.
func (s *Set) Harness(kind string) *ui.SVG {
	switch kind {
	case "claude":
		return s.Claude
	case "codex":
		return s.Codex
	case "hermes":
		return s.Hermes
	}
	return nil
}

// FolderType is the icon of a folder, in its own colors.
func (s *Set) FolderType() *ui.SVG { return s.files["folder-base"] }

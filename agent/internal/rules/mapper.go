package rules

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// FromClaude maps the tool name and tool input of a Claude Code PreToolUse call to a Call. A
// tool it does not know, or an input it cannot read, is KindOther (only `*` rules match); a
// read or write whose paths are missing has no paths (only `*` rules match).
func FromClaude(toolName string, input json.RawMessage, cwd, workspace string) Call {
	c := Call{Cwd: cwd, Workspace: workspace}
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Path         string `json:"path"`
		Pattern      string `json:"pattern"`
		URL          string `json:"url"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			c.Kind = KindOther
			return c
		}
	}
	one := func(p string) []string {
		if p == "" {
			return nil
		}
		return []string{p}
	}
	switch toolName {
	case "Bash":
		c.Kind, c.Command = KindRun, in.Command
	case "Read":
		c.Kind, c.Paths = KindRead, one(in.FilePath)
	case "NotebookRead":
		c.Kind, c.Paths = KindRead, one(in.NotebookPath)
	case "Write", "Edit", "MultiEdit":
		c.Kind, c.Paths = KindWrite, one(in.FilePath)
	case "NotebookEdit":
		c.Kind, c.Paths = KindWrite, one(in.NotebookPath)
	case "Grep":
		c.Kind = KindRead
		c.Paths = []string{dirOrCwd(in.Path, cwd, workspace)}
	case "Glob":
		c.Kind = KindRead
		dir := dirOrCwd(in.Path, cwd, workspace)
		c.Paths = []string{dir}
		if in.Pattern != "" {
			// The pattern is a path too: `**/.env` under dir, or an absolute pattern.
			if strings.HasPrefix(in.Pattern, "/") || strings.HasPrefix(in.Pattern, "~") {
				c.Paths = append(c.Paths, in.Pattern)
			} else {
				c.Paths = append(c.Paths, filepath.Join(absDir(dir, cwd), in.Pattern))
			}
		}
	case "WebFetch":
		c.Kind, c.URL = KindFetch, in.URL
	case "WebSearch":
		c.Kind = KindFetch
	default:
		if rest, ok := strings.CutPrefix(toolName, "mcp__"); ok {
			server, tool, found := strings.Cut(rest, "__")
			if found && server != "" && tool != "" {
				c.Kind, c.Server, c.Tool = KindMCP, server, tool
				return c
			}
		}
		c.Kind = KindOther
	}
	return c
}

func dirOrCwd(p, cwd, workspace string) string {
	switch {
	case p != "":
		return p
	case cwd != "":
		return cwd
	}
	return workspace
}

func absDir(dir, cwd string) string {
	if strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "~") {
		return dir
	}
	return filepath.Join(cwd, dir)
}

// FromCodexCommand maps a Codex commandExecution (its command line and cwd).
func FromCodexCommand(command, cwd, workspace string) Call {
	return Call{Kind: KindRun, Command: command, Cwd: cwd, Workspace: workspace}
}

// FromCodexFileChange maps a Codex fileChange. The paths come from the item announced before
// the approval request; with none the call matches only `*` rules.
func FromCodexFileChange(paths []string, cwd, workspace string) Call {
	return Call{Kind: KindWrite, Paths: paths, Cwd: cwd, Workspace: workspace}
}

// FromCodexMCP maps a Codex MCP tool call item.
func FromCodexMCP(server, tool, cwd, workspace string) Call {
	return Call{Kind: KindMCP, Server: server, Tool: tool, Cwd: cwd, Workspace: workspace}
}

package rules

import (
	"encoding/json"
	"strings"
)

// codexShellTools are the names under which a Codex PreToolUse hook carries, or is documented to carry, a shell
// command. Each is a KindRun call, so a rule about commands sees it whichever name Codex uses.
var codexShellTools = map[string]bool{
	"Bash": true, "shell": true, "shell_command": true, "exec_command": true, "local_shell": true,
	"container.exec": true, "unified_exec": true,
}

// FromCodexHook maps the tool name and tool input of a Codex PreToolUse call to a Call. A shell tool is a
// command (a command given as a list of words is quoted back into one line); `apply_patch` writes the files its
// patch names; `mcp__<server>__<tool>` is an MCP tool. Anything else, or an input it cannot read, is KindOther
// (only `*` rules match).
func FromCodexHook(toolName string, input json.RawMessage, cwd, workspace string) Call {
	c := Call{Cwd: cwd, Workspace: workspace, Kind: KindOther}
	var in struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return c
		}
	}
	switch {
	case codexShellTools[toolName]:
		c.Kind = KindRun
		var line string
		var words []string
		switch {
		case in.Cmd != "":
			line = in.Cmd
		case json.Unmarshal(in.Command, &line) == nil && line != "":
		case json.Unmarshal(in.Command, &words) == nil && len(words) > 0:
			line = quoteWords(words)
		}
		c.Command = line
	case toolName == "apply_patch" || toolName == "Edit" || toolName == "Write":
		c.Kind = KindWrite
		var patch string
		if json.Unmarshal(in.Command, &patch) == nil {
			c.Paths = patchPaths(patch)
		}
	default:
		if rest, ok := strings.CutPrefix(toolName, "mcp__"); ok {
			server, tool, found := strings.Cut(rest, "__")
			if found && server != "" && tool != "" {
				c.Kind, c.Server, c.Tool = KindMCP, server, tool
			}
		}
	}
	return c
}

// patchPaths are the files an `apply_patch` patch adds, updates, deletes or moves to.
func patchPaths(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if p, ok := strings.CutPrefix(line, prefix); ok && strings.TrimSpace(p) != "" {
				out = append(out, strings.TrimSpace(p))
			}
		}
	}
	return out
}

// quoteWords joins words into a shell line that reads back as the same words.
func quoteWords(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		plain := w != ""
		for _, r := range w {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./=:@%+,", r)) {
				plain = false
				break
			}
		}
		if plain {
			q[i] = w
			continue
		}
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

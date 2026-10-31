package claude

import (
	"encoding/json"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

// toolKind classifies claude's built-in tools.
func toolKind(name string) proto.ToolKind {
	switch name {
	case "Bash", "BashOutput", "KillShell", "KillBash":
		return proto.ToolExec
	case "Read", "Glob", "Grep", "LS":
		return proto.ToolRead
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return proto.ToolEdit
	case "WebFetch", "WebSearch":
		return proto.ToolWeb
	}
	return proto.ToolOther
}

// inputField reads one string field of a tool input.
func inputField(input json.RawMessage, key string) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// summary is a one-line description of a tool call.
func summary(name string, input json.RawMessage) string {
	key := map[string]string{
		"Bash": "command", "Read": "file_path", "Write": "file_path", "Edit": "file_path", "MultiEdit": "file_path",
		"NotebookEdit": "notebook_path", "Grep": "pattern", "Glob": "pattern", "WebFetch": "url", "WebSearch": "query",
		"Task": "description", "Agent": "description",
	}[name]
	if key == "" {
		return ""
	}
	s, _, _ := strings.Cut(inputField(input, key), "\n")
	return s
}

// filePath is the file a successful file tool changed.
func filePath(name string, input json.RawMessage) string {
	switch name {
	case "Write", "Edit", "MultiEdit":
		return inputField(input, "file_path")
	case "NotebookEdit":
		return inputField(input, "notebook_path")
	}
	return ""
}

// resultText is a tool result's text, whether a string or text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, x := range blocks {
			b.WriteString(x.Text)
		}
		return b.String()
	}
	return ""
}

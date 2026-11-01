package hermes

import (
	"encoding/json"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

// toolKind classifies Hermes's built-in tools.
func toolKind(name string) proto.ToolKind {
	switch name {
	case "terminal", "execute_code", "process":
		return proto.ToolExec
	case "read_file", "search_files":
		return proto.ToolRead
	case "write_file", "patch":
		return proto.ToolEdit
	case "web_search", "web_extract", "browser_exec":
		return proto.ToolWeb
	}
	return proto.ToolOther
}

// editPath is the file an edit tool writes, as its arguments name it.
func editPath(name string, args json.RawMessage) string {
	if name != "write_file" && name != "patch" {
		return ""
	}
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil {
		return ""
	}
	return a.Path
}

// oneLine is the first line of s.
func oneLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}

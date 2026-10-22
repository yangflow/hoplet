package rules

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFromCodexHook(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+x\n*** Update File: b/c.go\n*** Move to: b/d.go\n*** Delete File: e\n*** End Patch"
	patchJSON, _ := json.Marshal(patch)
	cases := []struct {
		name  string
		tool  string
		input string
		kind  Kind
		check func(Call) bool
	}{
		{"bash string", "Bash", `{"command":"ls -l"}`, KindRun, func(c Call) bool { return c.Command == "ls -l" }},
		{"shell words", "shell", `{"command":["git","commit","-m","a b"]}`, KindRun, func(c Call) bool { return c.Command == `git commit -m 'a b'` }},
		{"exec_command cmd", "exec_command", `{"cmd":"pwd"}`, KindRun, func(c Call) bool { return c.Command == "pwd" }},
		{"patch", "apply_patch", `{"command":` + string(patchJSON) + `}`, KindWrite, func(c Call) bool {
			return reflect.DeepEqual(c.Paths, []string{"a.txt", "b/c.go", "b/d.go", "e"})
		}},
		{"mcp", "mcp__github__create_issue", `{}`, KindMCP, func(c Call) bool { return c.Server == "github" && c.Tool == "create_issue" }},
		{"mcp without tool", "mcp__github", `{}`, KindOther, func(Call) bool { return true }},
		{"unknown tool", "web_search", `{"query":"x"}`, KindOther, func(Call) bool { return true }},
		{"unreadable input", "Bash", `[1]`, KindOther, func(Call) bool { return true }},
		{"shell without command", "Bash", `{}`, KindRun, func(c Call) bool { return c.Command == "" }},
	}
	for _, tc := range cases {
		c := FromCodexHook(tc.tool, []byte(tc.input), "/cwd", "/ws")
		if c.Kind != tc.kind || !tc.check(c) || c.Cwd != "/cwd" || c.Workspace != "/ws" {
			t.Errorf("%s: %+v", tc.name, c)
		}
	}
}

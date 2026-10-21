package rules

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFromClaude(t *testing.T) {
	cases := []struct {
		tool  string
		input string
		kind  Kind
		check func(Call) bool
	}{
		{"Bash", `{"command":"ls -l"}`, KindRun, func(c Call) bool { return c.Command == "ls -l" }},
		{"Read", `{"file_path":"/a/b"}`, KindRead, func(c Call) bool { return reflect.DeepEqual(c.Paths, []string{"/a/b"}) }},
		{"NotebookRead", `{"notebook_path":"/a.ipynb"}`, KindRead, func(c Call) bool { return len(c.Paths) == 1 }},
		{"Write", `{"file_path":"/a/b","content":"x"}`, KindWrite, func(c Call) bool { return len(c.Paths) == 1 }},
		{"Edit", `{"file_path":"/a/b"}`, KindWrite, func(c Call) bool { return len(c.Paths) == 1 }},
		{"MultiEdit", `{"file_path":"/a/b"}`, KindWrite, func(c Call) bool { return len(c.Paths) == 1 }},
		{"NotebookEdit", `{"notebook_path":"/a.ipynb"}`, KindWrite, func(c Call) bool { return len(c.Paths) == 1 }},
		{"Write", `{}`, KindWrite, func(c Call) bool { return len(c.Paths) == 0 }},
		{"Grep", `{"pattern":"x"}`, KindRead, func(c Call) bool { return reflect.DeepEqual(c.Paths, []string{"/cwd"}) }},
		{"Grep", `{"pattern":"x","path":"src"}`, KindRead, func(c Call) bool { return reflect.DeepEqual(c.Paths, []string{"src"}) }},
		{"Glob", `{"pattern":"**/.env"}`, KindRead, func(c Call) bool { return reflect.DeepEqual(c.Paths, []string{"/cwd", "/cwd/**/.env"}) }},
		{"Glob", `{"pattern":"/etc/*","path":"/x"}`, KindRead, func(c Call) bool { return reflect.DeepEqual(c.Paths, []string{"/x", "/etc/*"}) }},
		{"WebFetch", `{"url":"https://a.com"}`, KindFetch, func(c Call) bool { return c.URL == "https://a.com" }},
		{"WebSearch", `{"query":"x"}`, KindFetch, func(c Call) bool { return c.URL == "" }},
		{"mcp__github__create_issue", `{}`, KindMCP, func(c Call) bool { return c.Server == "github" && c.Tool == "create_issue" }},
		{"mcp__a__b__c", `{}`, KindMCP, func(c Call) bool { return c.Server == "a" && c.Tool == "b__c" }},
		{"mcp__broken", `{}`, KindOther, nil},
		{"Task", `{}`, KindOther, nil},
		{"Bash", `not json`, KindOther, nil},
	}
	for _, tc := range cases {
		c := FromClaude(tc.tool, json.RawMessage(tc.input), "/cwd", "/ws")
		if c.Kind != tc.kind || (tc.check != nil && !tc.check(c)) {
			t.Errorf("%s %s: %+v", tc.tool, tc.input, c)
		}
	}
}

func TestGlobPatternIsCheckedAsAPath(t *testing.T) {
	e := mustEngine(t, testEnv(), nil, Defaults())
	c := FromClaude("Glob", json.RawMessage(`{"pattern":"**/.env"}`), "/ws", "/ws")
	if d := e.Decide(c); d.RuleID != "default.secret-files" {
		t.Errorf("%+v", d)
	}
	c = FromClaude("Bash", json.RawMessage(`{"command":"git push -f"}`), "/ws", "/ws")
	if d := e.Decide(c); d.Action != Ask {
		t.Errorf("%+v", d)
	}
}

func TestFromCodex(t *testing.T) {
	if c := FromCodexCommand("ls", "/c", "/w"); c.Kind != KindRun || c.Command != "ls" || c.Cwd != "/c" || c.Workspace != "/w" {
		t.Errorf("%+v", c)
	}
	if c := FromCodexFileChange([]string{"/a"}, "/c", "/w"); c.Kind != KindWrite || len(c.Paths) != 1 {
		t.Errorf("%+v", c)
	}
	if c := FromCodexMCP("s", "t", "/c", "/w"); c.Kind != KindMCP || c.Server != "s" || c.Tool != "t" {
		t.Errorf("%+v", c)
	}
}

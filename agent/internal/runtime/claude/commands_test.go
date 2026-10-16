package claude

import (
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func names(list []runtime.Command) string {
	var out []string
	for _, c := range list {
		out = append(out, c.Kind[:1]+":"+c.Source[:1]+":"+c.Name)
	}
	return strings.Join(out, " ")
}

func TestBuildCommandsParsesSourcesAndKinds(t *testing.T) {
	raw := []rawCommand{
		{Name: "zeta", Description: "Last (project)"},
		{Name: "deploy", Description: "Deploy the app (project)", ArgumentHint: "<env>"},
		{Name: "fix-bug", Description: "Fix (user)"},
		{Name: "pdf:extract", Description: "Extract text (plugin)"},
		{Name: "compact", Description: "Summarize", Builtin: true},
		{Name: "simplify", Description: "Bundled skill"},
	}
	got := buildCommands(raw, map[string]bool{"fix-bug": true, "pdf:extract": true, "simplify": true})
	want := "c:b:compact c:p:deploy c:p:zeta s:u:fix-bug s:p:pdf:extract s:b:simplify"
	if names(got) != want {
		t.Fatalf("got %q, want %q", names(got), want)
	}
	byName := map[string]runtime.Command{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if d := byName["deploy"]; d.Description != "Deploy the app" || d.ArgumentHint != "<env>" {
		t.Fatalf("deploy = %+v", d)
	}
	if p := byName["pdf:extract"]; p.Plugin != "pdf" || p.Description != "Extract text" {
		t.Fatalf("plugin entry = %+v", p)
	}
	if byName["deploy"].Plugin != "" {
		t.Fatalf("plugin set on a project command")
	}
}

func TestBuildCommandsOffersOnlyWhitelistedBuiltins(t *testing.T) {
	var raw []rawCommand
	for _, n := range []string{"clear", "model", "help", "doctor", "color", "focus", "reload-plugins", "compact", "usage", "release-notes", "security-review"} {
		raw = append(raw, rawCommand{Name: n, Builtin: true})
	}
	raw = append(raw, rawCommand{Name: "mine", Description: "x (user)"})
	got := names(buildCommands(raw, nil))
	want := "c:b:compact c:u:mine c:b:release-notes c:b:security-review c:b:usage"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildCommandsBoundsTheList(t *testing.T) {
	var raw []rawCommand
	for i := 0; i < maxCommands+20; i++ {
		raw = append(raw, rawCommand{Name: "c" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), Description: strings.Repeat("é", 400) + " (user)"})
	}
	raw = append(raw, rawCommand{Name: strings.Repeat("n", 129)}, rawCommand{Name: ""})
	got := buildCommands(raw, nil)
	if len(got) != maxCommands {
		t.Fatalf("len = %d", len(got))
	}
	for _, c := range got {
		if len([]rune(c.Description)) != maxDescriptionChars {
			t.Fatalf("description has %d chars", len([]rune(c.Description)))
		}
	}
}

func TestClipCountsUTF16Units(t *testing.T) {
	if got := clip("ab😀cd", 3); got != "ab" {
		t.Fatalf("clip = %q", got)
	}
	if got := clip("ab😀cd", 4); got != "ab😀" {
		t.Fatalf("clip = %q", got)
	}
}

func TestCommandsComeFromInitializeSkillsAndCommandsChanged(t *testing.T) {
	bin := script(t, `
read init
echo '{"type":"control_response","response":{"subtype":"success","request_id":"hoplet-init","response":{"commands":[{"name":"fix-bug","description":"Fix (user)"},{"name":"clear","description":"Clear","builtin":true}]}}}'
echo '{"type":"system","subtype":"init","skills":["fix-bug"],"slash_commands":["fix-bug","clear"]}'
echo '{"type":"system","subtype":"commands_changed","commands":[{"name":"fix-bug","description":"Fix (user)"},{"name":"deploy","description":"Deploy (project)"}]}'
`)
	var lists []string
	for _, ev := range collect(t, start(t, bin)) {
		if c, ok := ev.(runtime.Commands); ok {
			lists = append(lists, names(c.List))
		}
	}
	want := []string{"c:u:fix-bug", "s:u:fix-bug", "c:p:deploy s:u:fix-bug"}
	if strings.Join(lists, "|") != strings.Join(want, "|") {
		t.Fatalf("lists = %q, want %q", lists, want)
	}
}

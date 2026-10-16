package claude

import (
	"regexp"
	"sort"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// Bounds of the list the agent reports; shared/src/commands.ts holds the same numbers.
const (
	maxCommands         = 500
	maxDescriptionChars = 300
	maxNameChars        = 128
)

// offeredBuiltins are the built-in commands of Claude Code that make sense in a prompt sent from Hoplet, the
// only built-ins reported (shared/src/commands.ts, BUILTIN_COMMANDS). Commands that change the CLI's own
// interface or the session's settings (clear, model, help, doctor, color, focus, reload-plugins) are left out.
var offeredBuiltins = map[string]bool{
	"compact": true, "context": true, "usage": true, "cost": true, "init": true,
	"review": true, "security-review": true, "pr-comments": true, "release-notes": true,
}

// rawCommand is one entry of the initialize answer or of system/commands_changed.
type rawCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
	Builtin      bool   `json:"builtin"`
}

// sourceSuffix is how Claude Code marks where a custom command, skill or plugin entry comes from.
var sourceSuffix = regexp.MustCompile(`\s*\((project|user|plugin)\)\s*$`)

// clip cuts s to at most n UTF-16 code units, the unit the wire's length limits count, on a rune boundary.
func clip(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// buildCommands turns Claude Code's list into the list Hoplet reports: built-ins only from the whitelist, the
// source taken from the description's suffix, entries named in skills marked as skills, then commands before
// skills, each by name, at most maxCommands.
func buildCommands(raw []rawCommand, skills map[string]bool) []runtime.Command {
	out := make([]runtime.Command, 0, len(raw))
	seen := map[string]bool{}
	for _, r := range raw {
		name := strings.TrimPrefix(strings.TrimSpace(r.Name), "/")
		if name == "" || clip(name, maxNameChars) != name || seen[name] {
			continue
		}
		c := runtime.Command{Name: name, Kind: "command", ArgumentHint: clip(r.ArgumentHint, maxNameChars)}
		desc := r.Description
		if m := sourceSuffix.FindStringSubmatch(desc); m != nil {
			c.Source = m[1]
			desc = strings.TrimSpace(sourceSuffix.ReplaceAllString(desc, ""))
		}
		c.Description = clip(desc, maxDescriptionChars)
		isSkill := skills[name]
		if isSkill {
			c.Kind = "skill"
		}
		switch {
		case c.Source == "" && r.Builtin:
			c.Source = "builtin"
		case c.Source == "" && strings.Contains(name, ":"):
			c.Source = "plugin"
		case c.Source == "":
			// A skill bundled with Claude Code, or a command without a mark.
			c.Source = "builtin"
		}
		if c.Source == "plugin" {
			if i := strings.IndexByte(name, ':'); i > 0 {
				c.Plugin = clip(name[:i], maxNameChars)
			}
		}
		if r.Builtin && !isSkill && !offeredBuiltins[name] {
			continue
		}
		seen[name] = true
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "command"
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > maxCommands {
		out = out[:maxCommands]
	}
	return out
}

// Package rules decides a tool call against permission rules (ADR 0029). It is a pure
// decision engine: it takes a provider-neutral Call and a rule set and answers allow, ask,
// deny or no decision. It reads the file system only to resolve symlinks, and it does not
// know about providers, the wire protocol or the core.
package rules

import (
	"fmt"
	"strings"
)

// Action is what a rule does.
type Action string

const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

// Scope says which sessions a rule applies to. ScopeAgent is only for the agent-local floor.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
	ScopeSession Scope = "session"
	ScopeAgent   Scope = "agent"
)

// Kind is the fixed set of tool kinds a call is placed in.
type Kind string

const (
	KindRun   Kind = "run"
	KindRead  Kind = "read"
	KindWrite Kind = "write"
	KindFetch Kind = "fetch"
	KindMCP   Kind = "mcp"
	KindOther Kind = "other"
)

// Rule is one permission rule. The caller passes only the rules that apply to the session
// (global, its project, the session itself); the engine does not look at project or session IDs.
//
// Tool is `run`, `read`, `write`, `fetch`, `mcp:<server>/<tool>` (either part may be `*`) or `*`.
// Built-in rules that cover two kinds write them as a comma list: `read,write`.
type Rule struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Match   Match  `json:"match"`
	Action  Action `json:"action"`
	Scope   Scope  `json:"scope"`
	Note    string `json:"note"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin"`
}

// Match is the matcher of a rule; which fields are allowed depends on the tool. An empty
// matcher matches every call of the tool. A rule with Or matches when its own fields or any
// alternative match.
type Match struct {
	// run: a prefix of words (`*` inside a word is any characters), arguments one of which
	// must match some later word, and the command names that must receive its output.
	Command string   `json:"command,omitempty"`
	AnyArg  []string `json:"anyArg,omitempty"`
	PipedTo []string `json:"pipedTo,omitempty"`
	// read, write: absolute globs (`*` in a segment, `**` across segments, `~`, `{workspace}`,
	// `{hoplet_home}`, `{tmp}`), minus the Except globs.
	Paths  []string `json:"paths,omitempty"`
	Except []string `json:"except,omitempty"`
	// fetch: an exact host or `*.example.com`.
	Domains []string `json:"domains,omitempty"`
	Or      []Match  `json:"or,omitempty"`
}

type toolSel struct {
	any    bool
	kind   Kind
	server string
	tool   string
}

func parseTool(spec string) ([]toolSel, error) {
	var out []toolSel
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "*":
			out = append(out, toolSel{any: true})
		case part == "run", part == "read", part == "write", part == "fetch":
			out = append(out, toolSel{kind: Kind(part)})
		case strings.HasPrefix(part, "mcp:"):
			server, tool, ok := strings.Cut(part[4:], "/")
			if !ok || server == "" || tool == "" || strings.Contains(tool, "/") {
				return nil, fmt.Errorf("tool %q: want mcp:<server>/<tool>", part)
			}
			out = append(out, toolSel{kind: KindMCP, server: server, tool: tool})
		default:
			return nil, fmt.Errorf("unknown tool %q", part)
		}
	}
	if len(out) > 1 {
		for _, s := range out {
			if s.any {
				return nil, fmt.Errorf("tool * cannot be combined")
			}
		}
	}
	return out, nil
}

type family int

const (
	famNone family = iota
	famRun
	famPath
	famFetch
)

func familyOf(sels []toolSel) (family, error) {
	f := famNone
	for _, s := range sels {
		g := famNone
		switch {
		case s.any, s.kind == KindMCP:
		case s.kind == KindRun:
			g = famRun
		case s.kind == KindRead, s.kind == KindWrite:
			g = famPath
		case s.kind == KindFetch:
			g = famFetch
		}
		if g == famNone {
			continue
		}
		if f != famNone && f != g {
			return 0, fmt.Errorf("a rule cannot mix tools with different matchers")
		}
		f = g
	}
	return f, nil
}

// Validate checks a rule that arrives from outside. The agent applies a set whole or not at all.
func (r Rule) Validate() error { return r.validate(false) }

func (r Rule) validate(floor bool) error {
	err := func() error {
		if r.ID == "" {
			return fmt.Errorf("no id")
		}
		switch r.Action {
		case Allow, Ask, Deny:
		default:
			return fmt.Errorf("unknown action %q", r.Action)
		}
		switch r.Scope {
		case ScopeGlobal, ScopeProject, ScopeSession:
		case ScopeAgent:
			if !floor {
				return fmt.Errorf("scope agent is not pushable")
			}
		default:
			return fmt.Errorf("unknown scope %q", r.Scope)
		}
		sels, err := parseTool(r.Tool)
		if err != nil {
			return err
		}
		fam, err := familyOf(sels)
		if err != nil {
			return err
		}
		return validateMatch(r.Match, fam, 0)
	}()
	if err != nil {
		return fmt.Errorf("rule %q: %w", r.ID, err)
	}
	return nil
}

func validateMatch(m Match, fam family, depth int) error {
	if depth > 2 {
		return fmt.Errorf("match nested too deep")
	}
	run := m.Command != "" || len(m.AnyArg) > 0 || len(m.PipedTo) > 0
	path := len(m.Paths) > 0 || len(m.Except) > 0
	dom := len(m.Domains) > 0
	if (run && fam != famRun) || (path && fam != famPath) || (dom && fam != famFetch) {
		return fmt.Errorf("matcher does not fit the tool")
	}
	if (len(m.AnyArg) > 0 || len(m.PipedTo) > 0) && len(strings.Fields(m.Command)) == 0 {
		return fmt.Errorf("anyArg and pipedTo need a command")
	}
	if len(m.Except) > 0 && len(m.Paths) == 0 {
		return fmt.Errorf("except needs paths")
	}
	for _, p := range append(append([]string{}, m.Paths...), m.Except...) {
		if !validPattern(p) {
			return fmt.Errorf("path %q is not absolute", p)
		}
	}
	for _, d := range m.Domains {
		if !validDomain(d) {
			return fmt.Errorf("domain %q", d)
		}
	}
	for _, o := range m.Or {
		if err := validateMatch(o, fam, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validPattern(p string) bool {
	if p == "" {
		return false
	}
	return p[0] == '/' || p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "**") ||
		strings.HasPrefix(p, "{workspace}") || strings.HasPrefix(p, "{hoplet_home}") || strings.HasPrefix(p, "{tmp}")
}

func (m Match) hasOwn() bool {
	return m.Command != "" || len(m.AnyArg) > 0 || len(m.PipedTo) > 0 || len(m.Paths) > 0 ||
		len(m.Except) > 0 || len(m.Domains) > 0
}

// Call is a tool call in provider-neutral form.
type Call struct {
	Kind Kind
	// Command is the shell command of a KindRun call.
	Command string
	// Paths are the paths of a KindRead or KindWrite call, as written. No paths means the
	// driver could not know them, and only `*` rules match.
	Paths []string
	// URL is the address of a KindFetch call; empty for a web search.
	URL string
	// Server and Tool name an MCP tool.
	Server, Tool string
	// Cwd is the session's working directory; Workspace its root (the project directory, or the
	// worktree a handoff made).
	Cwd, Workspace string
}

// Decision is the result. Action is empty when no rule decided and the mode decides.
type Decision struct {
	Action Action
	RuleID string
	Scope  Scope
	Reason string
}

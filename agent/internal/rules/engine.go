package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Env is what the engine needs to know about the machine.
type Env struct {
	// Home is the user's home directory (`~`); HopletHome the agent's state directory
	// (`{hoplet_home}`); Tmp the operating system's temporary directory (`{tmp}`).
	Home, HopletHome, Tmp string
	// FoldCase compares paths without regard to case: the file systems of macOS and Windows.
	FoldCase bool
}

// NewEnv returns the Env of this machine.
func NewEnv(home, hopletHome string) Env {
	return Env{
		Home:       home,
		HopletHome: hopletHome,
		Tmp:        os.TempDir(),
		FoldCase:   runtime.GOOS == "darwin" || runtime.GOOS == "windows",
	}
}

type compiled struct {
	Rule
	sels []toolSel
}

// Engine decides calls against one rule set. It holds no state between calls and is safe for
// concurrent use.
type Engine struct {
	env   Env
	rules []compiled
	dirs  map[string][]string // the directories of the path tokens, resolved once
	// revision is the revision of the pushed set the engine was built from; 0 when none.
	revision int
}

// Revision is the revision of the pushed set the engine was built from; 0 for an engine built without one.
func (e *Engine) Revision() int { return e.revision }

// New builds an engine from the compiled-in floor, the operator's local deny rules
// (`permission_deny`, scope forced to agent, deny only) and the rules the core pushed. It
// refuses the whole set when one rule is invalid or an ID repeats.
func New(env Env, local, pushed []Rule) (*Engine, error) {
	e := &Engine{env: env, dirs: map[string][]string{
		"~":             dirVariants(env.Home),
		"{hoplet_home}": dirVariants(env.HopletHome),
		"{tmp}":         dirVariants(env.Tmp),
	}}
	seen := map[string]bool{}
	add := func(r Rule, floor bool) error {
		if err := r.validate(floor); err != nil {
			return err
		}
		if seen[r.ID] {
			return fmt.Errorf("rule %q: duplicate id", r.ID)
		}
		seen[r.ID] = true
		sels, _ := parseTool(r.Tool)
		e.rules = append(e.rules, compiled{Rule: r, sels: sels})
		return nil
	}
	if err := add(Floor(), true); err != nil {
		return nil, err
	}
	for _, r := range local {
		if r.Action != Deny {
			return nil, fmt.Errorf("rule %q: a local rule can only deny", r.ID)
		}
		r.Scope = ScopeAgent
		r.Enabled = true
		if err := add(r, true); err != nil {
			return nil, err
		}
	}
	for _, r := range pushed {
		if err := add(r, false); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func scopeRank(s Scope) int {
	switch s {
	case ScopeSession:
		return 0
	case ScopeProject:
		return 1
	}
	return 2
}

// Decide returns the decision of the rules for a call, or a Decision with an empty Action when
// no rule decides and the session's permission mode does.
func (e *Engine) Decide(c Call) Decision {
	x := e.prepare(c)
	var deny, best *compiled
	for i := range e.rules {
		r := &e.rules[i]
		if !r.Enabled || !x.matches(r) {
			continue
		}
		if r.Action == Deny {
			if deny == nil {
				deny = r
			}
			continue
		}
		if best == nil || scopeRank(r.Scope) < scopeRank(best.Scope) ||
			(scopeRank(r.Scope) == scopeRank(best.Scope) && r.Action == Ask && best.Action == Allow) {
			best = r
		}
	}
	switch {
	case deny != nil:
		return decision(deny)
	case best != nil:
		return decision(best)
	}
	return Decision{}
}

func decision(r *compiled) Decision {
	name := r.Note
	if name == "" {
		name = r.ID
	}
	verb := map[Action]string{Allow: "Allowed", Ask: "Asked", Deny: "Denied"}[r.Action]
	return Decision{
		Action: r.Action,
		RuleID: r.ID,
		Scope:  r.Scope,
		Reason: fmt.Sprintf("%s by rule %q (%s)", verb, name, r.Scope),
	}
}

type pathPair struct{ written, resolved string }

type callCtx struct {
	e *Engine
	c Call

	// run
	script  script
	views   []view
	allowOK bool
	words   []pathPair // path-like words of every view
	rdRedir []pathPair // read redirections
	wrRedir []pathPair // write redirections

	// read, write
	paths []pathPair

	// fetch
	host   string
	hostOK bool

	expanded map[string][]string
}

func (e *Engine) prepare(c Call) *callCtx {
	x := &callCtx{e: e, c: c, expanded: map[string][]string{}}
	home := e.env.Home
	switch c.Kind {
	case KindRun:
		x.script = parseScript(c.Command, 0, &pstate{})
		x.views = x.script.views(0)
		x.allowOK = len(x.script.control) == 0 && !x.script.unreadable && len(x.script.cmds) == 1 &&
			len(x.script.cmds[0].argv) > 0
		seen := map[string]bool{}
		resolve := func(p string) pathPair {
			w, r := ResolvePath(p, x.base(), home)
			return pathPair{w, r}
		}
		for _, v := range x.views {
			for i, a := range v.argv {
				if i == 0 {
					continue
				}
				if _, val, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "-") {
					a = val
				}
				if looksLikePath(a) && !seen["w"+a] {
					seen["w"+a] = true
					x.words = append(x.words, resolve(a))
				}
			}
			for _, r := range v.redirs {
				if r.heredoc || r.op == "<<<" || r.target == "" || isStdStream(r.target) {
					continue
				}
				if (r.op == ">&" || r.op == "<&") && (r.target == "-" || strings.Trim(r.target, "0123456789") == "") {
					continue
				}
				key := r.op + r.target
				if seen[key] {
					continue
				}
				seen[key] = true
				if r.write {
					x.wrRedir = append(x.wrRedir, resolve(r.target))
				} else {
					x.rdRedir = append(x.rdRedir, resolve(r.target))
				}
			}
		}
	case KindRead, KindWrite:
		for _, p := range c.Paths {
			if p == "" {
				continue
			}
			w, r := ResolvePath(p, x.base(), home)
			x.paths = append(x.paths, pathPair{w, r})
		}
	case KindFetch:
		if c.URL != "" {
			x.host, x.hostOK = hostOf(c.URL)
		}
	}
	return x
}

func (x *callCtx) base() string {
	if x.c.Cwd != "" {
		return x.c.Cwd
	}
	if x.c.Workspace != "" {
		return x.c.Workspace
	}
	return "/"
}

// isStdStream is a redirection target that is not a file: 2>/dev/null is in most commands.
func isStdStream(p string) bool {
	return p == "/dev/null" || p == "/dev/stdout" || p == "/dev/stderr" || p == "/dev/tty"
}

func looksLikePath(w string) bool {
	return strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") || strings.HasPrefix(w, ".") || strings.Contains(w, "/")
}

func selMatches(s toolSel, c Call) bool {
	switch {
	case s.any:
		return true
	case s.kind == KindMCP:
		return c.Kind == KindMCP && wildcard(s.server, c.Server, false) && wildcard(s.tool, c.Tool, false)
	}
	return s.kind == c.Kind
}

func (x *callCtx) matches(r *compiled) bool {
	c := x.c
	var anySel, kindSel, readSel, writeSel bool
	for _, s := range r.sels {
		switch {
		case s.any:
			anySel = true
		case s.kind == KindRead:
			readSel = true
		case s.kind == KindWrite:
			writeSel = true
		}
		if selMatches(s, c) {
			kindSel = true
		}
	}
	allow := r.Action == Allow
	switch c.Kind {
	case KindRun:
		return x.matchRun(r, allow, kindSel || anySel, readSel, writeSel)
	case KindRead, KindWrite:
		if !kindSel {
			return false
		}
		if len(x.paths) == 0 {
			return anySel
		}
		for _, p := range x.paths {
			ok := x.pathMatch(r.Match, p, allow)
			if allow && !ok {
				return false
			}
			if !allow && ok {
				return true
			}
		}
		return allow
	case KindFetch:
		return kindSel && x.fetchMatch(r.Match)
	case KindMCP, KindOther:
		return kindSel
	}
	return false
}

// ---- run ----

func (x *callCtx) matchRun(r *compiled, allow, runSel, readSel, writeSel bool) bool {
	if allow {
		if !runSel || !x.allowOK {
			return false
		}
		c := x.script.cmds[0]
		return commandMatch(r.Match, view{argv: x.script.texts(c)}, true)
	}
	if runSel {
		for _, v := range x.views {
			if commandMatch(r.Match, v, false) {
				return true
			}
		}
	}
	var cands []pathPair
	if readSel {
		cands = append(cands, x.words...)
		cands = append(cands, x.rdRedir...)
	}
	if writeSel {
		cands = append(cands, x.wrRedir...)
	}
	for _, p := range cands {
		if x.pathMatch(r.Match, p, false) {
			return true
		}
	}
	return false
}

func commandMatch(m Match, v view, exactFirst bool) bool {
	if !m.hasOwn() && len(m.Or) == 0 {
		return true
	}
	if m.hasOwn() && commandOwn(m, v, exactFirst) {
		return true
	}
	for _, o := range m.Or {
		if commandMatch(o, v, exactFirst) {
			return true
		}
	}
	return false
}

func commandOwn(m Match, v view, exactFirst bool) bool {
	words := strings.Fields(m.Command)
	if len(v.argv) < len(words) {
		return false
	}
	for i, w := range words {
		a := v.argv[i]
		ok := wildcard(w, a, false)
		if !ok && i == 0 && !exactFirst && strings.Contains(a, "/") {
			ok = wildcard(w, filepath.Base(a), false)
		}
		if !ok {
			return false
		}
	}
	if len(m.AnyArg) > 0 {
		found := false
		for _, a := range v.argv[len(words):] {
			for _, g := range m.AnyArg {
				if wildcard(g, a, false) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	if len(m.PipedTo) > 0 {
		found := false
		for _, n := range v.piped {
			for _, g := range m.PipedTo {
				if wildcard(g, n, false) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ---- paths ----

// pathMatch tests a matcher against one path. An allow needs the resolved path to match; a deny
// or ask rule matches when the written or the resolved path does.
func (x *callCtx) pathMatch(m Match, p pathPair, resolvedOnly bool) bool {
	if !m.hasOwn() && len(m.Or) == 0 {
		return true
	}
	if m.hasOwn() {
		// An exception has to hold for the resolved path: a link inside the workspace that
		// leads out of it is not excepted by where it is written.
		if !x.globsAny(m.Except, p.resolved) &&
			(x.globsAny(m.Paths, p.resolved) || (!resolvedOnly && x.globsAny(m.Paths, p.written))) {
			return true
		}
	}
	for _, o := range m.Or {
		if x.pathMatch(o, p, resolvedOnly) {
			return true
		}
	}
	return false
}

func (x *callCtx) globsAny(globs []string, p string) bool {
	segs := splitSegs(p)
	for _, g := range globs {
		for _, v := range x.expand(g) {
			if globMatch(splitSegs(v), segs, x.e.env.FoldCase) {
				return true
			}
		}
	}
	return false
}

// expand turns the token a glob starts with into the directory it stands for, both as written
// and with symlinks resolved (so /tmp and /private/tmp both work). A token with no value gives
// no variant and the glob matches nothing.
func (x *callCtx) expand(g string) []string {
	if v, ok := x.expanded[g]; ok {
		return v
	}
	var out []string
	switch {
	case g == "~" || strings.HasPrefix(g, "~/"):
		out = join(x.e.dirs["~"], g[1:])
	case strings.HasPrefix(g, "{workspace}"):
		out = variants(x.c.Workspace, g[len("{workspace}"):])
	case strings.HasPrefix(g, "{hoplet_home}"):
		out = join(x.e.dirs["{hoplet_home}"], g[len("{hoplet_home}"):])
	case strings.HasPrefix(g, "{tmp}"):
		out = join(x.e.dirs["{tmp}"], g[len("{tmp}"):])
	default:
		out = []string{g}
	}
	x.expanded[g] = out
	return out
}

func variants(dir, rest string) []string {
	return join(dirVariants(dir), rest)
}

// dirVariants is a directory as given and with symlinks resolved; nothing for a directory that
// is not absolute.
func dirVariants(dir string) []string {
	if dir == "" || !strings.HasPrefix(dir, "/") {
		return nil
	}
	a := filepath.Clean(dir)
	out := []string{a}
	if b := physical(a); b != a {
		out = append(out, b)
	}
	return out
}

func join(dirs []string, rest string) []string {
	var out []string
	for _, d := range dirs {
		out = append(out, d+rest)
	}
	return out
}

// ---- fetch ----

func (x *callCtx) fetchMatch(m Match) bool {
	if !m.hasOwn() && len(m.Or) == 0 {
		return true
	}
	if len(m.Domains) > 0 && x.hostOK {
		for _, d := range m.Domains {
			if domainMatches(d, x.host) {
				return true
			}
		}
	}
	for _, o := range m.Or {
		if x.fetchMatch(o) {
			return true
		}
	}
	return false
}

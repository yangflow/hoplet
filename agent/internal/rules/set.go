package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SetFile is the name of the file in the agent's state directory that keeps the last applied Set.
const SetFile = "permission-rules.json"

// ProjectRule is a rule of one project of the device, named by the project's path.
type ProjectRule struct {
	ProjectPath string `json:"projectPath"`
	Rule        Rule   `json:"rule"`
}

// SessionRule is a rule of one session the device owns.
type SessionRule struct {
	SessionID string `json:"sessionId"`
	Rule      Rule   `json:"rule"`
}

// Set is what the core pushes to one device (`device.rules`) and what the agent keeps on disk: every rule that
// can apply to a session on that device. The built-in rules are in Global with their enabled state.
type Set struct {
	Revision int           `json:"revision"`
	Global   []Rule        `json:"global"`
	Projects []ProjectRule `json:"projects"`
	Sessions []SessionRule `json:"sessions"`
	// Inherited is what a child session obeys besides its own rules: the deny and ask rules of the sessions
	// that started it, by the child's session ID (ADR 0029). It is never an allow rule. A rule keeps the ID of
	// the rule it comes from, so the same ID may appear for several children.
	Inherited []SessionRule `json:"inherited"`
}

// DefaultSet is the set in force before the core pushed any: the built-in rules, all enabled.
func DefaultSet() Set {
	return Set{Global: Defaults(), Projects: []ProjectRule{}, Sessions: []SessionRule{}, Inherited: []SessionRule{}}
}

// ParseSet decodes a Set strictly: an unknown field anywhere, including inside a rule, is an error, and so is
// anything after the value.
func ParseSet(raw []byte) (Set, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Set
	if err := dec.Decode(&s); err != nil {
		return Set{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Set{}, errors.New("unexpected data after the rule set")
	}
	if s.Revision < 0 {
		return Set{}, errors.New("revision is negative")
	}
	if s.Global == nil {
		s.Global = []Rule{}
	}
	if s.Projects == nil {
		s.Projects = []ProjectRule{}
	}
	if s.Sessions == nil {
		s.Sessions = []SessionRule{}
	}
	if s.Inherited == nil {
		s.Inherited = []SessionRule{}
	}
	return s, nil
}

// checkInherited refuses an inherited rule that is not a deny or ask rule of the session
// scope for a named session: a child never gets an allow rule from its parent.
func (s Set) checkInherited() error {
	for _, x := range s.Inherited {
		if err := checkInheritable(x.Rule); err != nil {
			return err
		}
		if x.SessionID == "" {
			return fmt.Errorf("inherited rule %q: no session id", x.Rule.ID)
		}
	}
	return nil
}

// checkInheritable is the part of the check of an inherited rule that does not depend on the child.
func checkInheritable(r Rule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Scope != ScopeSession {
		return fmt.Errorf("inherited rule %q: scope %q, only session rules are inherited", r.ID, r.Scope)
	}
	if r.Action == Allow {
		return fmt.Errorf("inherited rule %q: a child does not inherit allow rules", r.ID)
	}
	return nil
}

// all lists every rule of the set, checking that each sits in the list of its own scope.
func (s Set) all() ([]Rule, error) {
	var out []Rule
	for _, r := range s.Global {
		if r.Scope != ScopeGlobal {
			return nil, fmt.Errorf("rule %q: scope %q in the global list", r.ID, r.Scope)
		}
		out = append(out, r)
	}
	for _, p := range s.Projects {
		if p.Rule.Scope != ScopeProject {
			return nil, fmt.Errorf("rule %q: scope %q in the project list", p.Rule.ID, p.Rule.Scope)
		}
		if !filepath.IsAbs(p.ProjectPath) {
			return nil, fmt.Errorf("rule %q: project path %q is not absolute", p.Rule.ID, p.ProjectPath)
		}
		out = append(out, p.Rule)
	}
	for _, x := range s.Sessions {
		if x.Rule.Scope != ScopeSession {
			return nil, fmt.Errorf("rule %q: scope %q in the session list", x.Rule.ID, x.Rule.Scope)
		}
		if x.SessionID == "" {
			return nil, fmt.Errorf("rule %q: no session id", x.Rule.ID)
		}
		out = append(out, x.Rule)
	}
	return out, nil
}

// MaxLocal is the most `permission_deny` rules; the core accepts no more in device.hello.
const MaxLocal = 100

// ValidateLocal checks the operator's `permission_deny` rules: each must be a valid deny rule with its own ID.
func ValidateLocal(local []Rule) error {
	if len(local) > MaxLocal {
		return fmt.Errorf("at most %d rules", MaxLocal)
	}
	_, err := New(NewEnv("", ""), local, nil)
	return err
}

// Announced is the operator's `permission_deny` rules as the engine holds them (scope agent, enabled, not a
// built-in): what the agent announces to the core. nil when there are none.
func Announced(local []Rule) []Rule {
	if len(local) == 0 {
		return nil
	}
	out := make([]Rule, len(local))
	for i, r := range local {
		r.Scope = ScopeAgent
		r.Enabled = true
		r.Builtin = false
		out[i] = r
	}
	return out
}

// Validate checks every rule and that no ID repeats, by building the engine of the whole set.
func (s Set) Validate(env Env, local []Rule) error {
	all, err := s.all()
	if err != nil {
		return err
	}
	if _, err = New(env, local, all); err != nil {
		return err
	}
	return s.checkInherited()
}

// ForSession is the rules that apply to one session: the global rules, the rules of the project that holds
// dir, the session's own and the ones it inherits. A project holds dir when dir is the project path or below it.
func (s Set) ForSession(sessionID, dir string) []Rule {
	out := append([]Rule{}, s.Global...)
	dir = filepath.Clean(dir)
	sep := string(filepath.Separator)
	for _, p := range s.Projects {
		root := filepath.Clean(p.ProjectPath)
		if dir == root || strings.HasPrefix(dir, strings.TrimSuffix(root, sep)+sep) {
			out = append(out, p.Rule)
		}
	}
	for _, x := range s.Sessions {
		if x.SessionID == sessionID {
			out = append(out, x.Rule)
		}
	}
	for _, x := range s.Inherited {
		if x.SessionID == sessionID {
			out = append(out, x.Rule)
		}
	}
	return out
}

// Store holds the Set in force on this agent. Apply replaces it and writes it to disk; a new Store starts from
// the file, so the rules hold while the core is unreachable. It is safe for concurrent use.
type Store struct {
	env   Env
	local []Rule
	path  string

	mu  sync.RWMutex
	set Set
	// pending holds the inherited rules of sessions that are starting, by the directory they run in, until the
	// session's ID is known; bound holds them by session ID until a pushed set carries them (Inherit).
	pending map[*Carry]struct{}
	bound   map[string][]Rule
}

// Carry is the inherited rules of one session that is starting. runtime.start brings them (ADR 0029): the
// session's ID is not known to the core before its process runs, and its first tool call must already obey them.
type Carry struct {
	st    *Store
	dir   string
	rules []Rule
}

// Inherit registers the deny and ask rules a session about to start in dir inherits. Until Bind or Drop they
// apply to every session in dir, which can only make a session stricter. It refuses an allow rule and a rule
// that is not of the session scope.
func (st *Store) Inherit(dir string, rs []Rule) (*Carry, error) {
	for _, r := range rs {
		if err := checkInheritable(r); err != nil {
			return nil, err
		}
	}
	c := &Carry{st: st, dir: filepath.Clean(dir), rules: append([]Rule{}, rs...)}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pending == nil {
		st.pending = map[*Carry]struct{}{}
	}
	st.pending[c] = struct{}{}
	return c, nil
}

// Bind ties the carried rules to the session's ID. They hold until a pushed set carries them for that session.
func (c *Carry) Bind(sessionID string) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	if _, ok := c.st.pending[c]; !ok {
		return
	}
	delete(c.st.pending, c)
	if c.st.bound == nil {
		c.st.bound = map[string][]Rule{}
	}
	c.st.bound[sessionID] = append(c.st.bound[sessionID], c.rules...)
}

// Drop forgets the carried rules of a start that failed. After Bind it does nothing.
func (c *Carry) Drop() {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	delete(c.st.pending, c)
}

// OpenStore loads the set kept in dir. A missing file means the built-in defaults; a file that cannot be read
// or no longer validates is logged and also means the defaults. local is the operator's `permission_deny`.
func OpenStore(env Env, dir string, local []Rule) *Store {
	st := &Store{env: env, local: local, path: filepath.Join(dir, SetFile), set: DefaultSet()}
	raw, err := os.ReadFile(st.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st
	case err != nil:
		log.Printf("[agent] the permission rules file cannot be read, using the built-in rules: %v", err)
		return st
	}
	s, err := ParseSet(raw)
	if err == nil {
		err = s.Validate(env, local)
	}
	if err != nil {
		log.Printf("[agent] the permission rules file is invalid, using the built-in rules: %v", err)
		return st
	}
	st.set = s
	return st
}

// Apply validates the pushed set, writes it and makes it the set in force. When anything fails the previous set
// stays in force, on disk and in memory. It returns the revision it read, when it could read one.
func (st *Store) Apply(raw []byte) (int, error) {
	s, err := ParseSet(raw)
	if err != nil {
		return 0, err
	}
	if err := s.Validate(st.env, st.local); err != nil {
		return s.Revision, err
	}
	body, err := json.Marshal(s)
	if err != nil {
		return s.Revision, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := writeAtomic(st.path, body); err != nil {
		return s.Revision, fmt.Errorf("write the rules file: %w", err)
	}
	st.set = s
	// The set carries the inherited rules of every child it knows; the ones held in memory are no longer needed.
	for _, x := range s.Inherited {
		delete(st.bound, x.SessionID)
	}
	return s.Revision, nil
}

// Revision is the revision of the set in force; 0 until the core pushed one.
func (st *Store) Revision() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.set.Revision
}

// Engine builds the engine of one session from the set in force, the floor and the operator's local rules.
func (st *Store) Engine(sessionID, dir string) (*Engine, error) {
	st.mu.RLock()
	rs := st.set.ForSession(sessionID, dir)
	revision := st.set.Revision
	seen := map[string]bool{}
	for _, r := range rs {
		seen[r.ID] = true
	}
	held := append([]Rule{}, st.bound[sessionID]...)
	clean := filepath.Clean(dir)
	for c := range st.pending {
		if c.dir == clean {
			held = append(held, c.rules...)
		}
	}
	for _, r := range held {
		if !seen[r.ID] {
			seen[r.ID] = true
			rs = append(rs, r)
		}
	}
	st.mu.RUnlock()
	e, err := New(st.env, st.local, rs)
	if err != nil {
		return nil, err
	}
	e.revision = revision
	return e, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".permission-rules-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

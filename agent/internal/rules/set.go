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
}

// DefaultSet is the set in force before the core pushed any: the built-in rules, all enabled.
func DefaultSet() Set {
	return Set{Global: Defaults(), Projects: []ProjectRule{}, Sessions: []SessionRule{}}
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
	return s, nil
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

// ValidateLocal checks the operator's `permission_deny` rules: each must be a valid deny rule with its own ID.
func ValidateLocal(local []Rule) error {
	_, err := New(NewEnv("", ""), local, nil)
	return err
}

// Validate checks every rule and that no ID repeats, by building the engine of the whole set.
func (s Set) Validate(env Env, local []Rule) error {
	all, err := s.all()
	if err != nil {
		return err
	}
	_, err = New(env, local, all)
	return err
}

// ForSession is the rules that apply to one session: the global rules, the rules of the project that holds
// dir, and the session's own. A project holds dir when dir is the project path or below it.
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
	st.mu.RUnlock()
	return New(st.env, st.local, rs)
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

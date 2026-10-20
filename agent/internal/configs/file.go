// Package configs holds the provider configurations of a machine (ADR 0028): <hoplet_home>/providers.json, the
// built-in configurations of the two vendors, the presets and the environment a configuration's process gets.
// Command and environment are written on the machine only; nothing here prints or logs a value.
package configs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const fileName = "providers.json"

// Vendors a configuration can extend; they are also the IDs of the built-in configurations.
const (
	Claude = "claude"
	Codex  = "codex"
)

// Errors a caller can tell apart.
var (
	ErrUnknown  = errors.New("unknown configuration")
	ErrBuiltIn  = errors.New("a built-in configuration cannot be changed this way")
	ErrExists   = errors.New("a configuration with this id exists")
	ErrInvalid  = errors.New("invalid value")
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	envPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	hopletEnvPf = "HOPLET_"
)

// Configuration is one entry: a vendor, optionally another binary and environment, and the defaults of a new
// session's options.
type Configuration struct {
	ID      string
	Name    string
	Extends string
	// Command is the absolute path of the executable; empty means the agent's claude_bin or codex_bin.
	Command string
	// Env maps a variable name to its value. An empty value means the variable is named but not set here.
	Env      map[string]string
	Defaults map[string]any
	Enabled  bool
}

// BuiltIn says whether id names one of the configurations that always exist.
func BuiltIn(id string) bool { return id == Claude || id == Codex }

// ValidID says whether id is a slug.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// ValidEnvName says whether name can be a variable of a configuration: a plain identifier the agent itself does
// not use (HOPLET_*).
func ValidEnvName(name string) bool {
	return envPattern.MatchString(name) && !strings.HasPrefix(name, hopletEnvPf)
}

// File is the content of providers.json: the configurations the owner added and the overrides of the
// built-ins (name, enabled, defaults).
type File struct {
	items []Configuration
}

type diskItem struct {
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	Extends  string            `json:"extends,omitempty"`
	Command  string            `json:"command,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Defaults map[string]any    `json:"defaults,omitempty"`
	Enabled  *bool             `json:"enabled,omitempty"`
}

// Path is where the file of the agent home lives.
func Path(home string) string { return filepath.Join(home, fileName) }

// Load reads providers.json; a missing file is an empty one.
func Load(home string) (*File, error) {
	raw, err := os.ReadFile(Path(home))
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, fmt.Errorf("read providers: %w", err)
	}
	var disk struct {
		Providers []diskItem `json:"providers"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		return nil, fmt.Errorf("parse providers: %w", err)
	}
	f := &File{}
	seen := map[string]bool{}
	for i, it := range disk.Providers {
		c, err := fromDisk(it)
		if err != nil {
			return nil, fmt.Errorf("parse providers: providers[%d]: %w", i, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("parse providers: providers[%d]: %w", i, ErrExists)
		}
		seen[c.ID] = true
		f.items = append(f.items, c)
	}
	return f, nil
}

func fromDisk(it diskItem) (Configuration, error) {
	if !ValidID(it.ID) {
		return Configuration{}, fmt.Errorf("%w: id", ErrInvalid)
	}
	c := Configuration{ID: it.ID, Name: it.Name, Extends: it.Extends, Command: it.Command, Env: it.Env, Defaults: it.Defaults, Enabled: it.Enabled == nil || *it.Enabled}
	if BuiltIn(c.ID) {
		c.Extends = c.ID
		if c.Command != "" || len(c.Env) > 0 {
			return Configuration{}, fmt.Errorf("%s: %w", c.ID, ErrBuiltIn)
		}
	}
	if c.Extends != Claude && c.Extends != Codex {
		return Configuration{}, fmt.Errorf("%w: extends", ErrInvalid)
	}
	if err := checkFields(c); err != nil {
		return Configuration{}, err
	}
	if c.Name == "" {
		c.Name = defaultName(c)
	}
	if c.Env == nil {
		c.Env = map[string]string{}
	}
	if c.Defaults == nil {
		c.Defaults = map[string]any{}
	}
	return c, nil
}

func checkFields(c Configuration) error {
	if c.Command != "" && !filepath.IsAbs(c.Command) {
		return fmt.Errorf("%w: command must be an absolute path", ErrInvalid)
	}
	if len(c.Name) > 64 {
		return fmt.Errorf("%w: name is longer than 64 bytes", ErrInvalid)
	}
	for name := range c.Env {
		if !ValidEnvName(name) {
			return fmt.Errorf("%w: environment variable name %q", ErrInvalid, name)
		}
	}
	return nil
}

func defaultName(c Configuration) string {
	switch c.ID {
	case Claude:
		return "Claude Code"
	case Codex:
		return "Codex"
	}
	return c.ID
}

// Save writes the file atomically with mode 0600: the content goes to a temporary file in the same directory,
// which then replaces providers.json.
func (f *File) Save(home string) error {
	disk := struct {
		Providers []diskItem `json:"providers"`
	}{Providers: []diskItem{}}
	for _, c := range f.items {
		enabled := c.Enabled
		it := diskItem{ID: c.ID, Name: c.Name, Command: c.Command, Env: nilIfEmpty(c.Env), Defaults: c.Defaults, Enabled: &enabled}
		if len(c.Defaults) == 0 {
			it.Defaults = nil
		}
		if !BuiltIn(c.ID) {
			it.Extends = c.Extends
		} else if c.Name == defaultName(c) {
			it.Name = ""
		}
		disk.Providers = append(disk.Providers, it)
	}
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(home, fileName, append(raw, '\n'))
}

// writeAtomic writes data to home/name with mode 0600: the content goes to a temporary file in the same
// directory, which then replaces the target.
func writeAtomic(home, name string, data []byte) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(home, name+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(home, name)); err != nil {
		cleanup()
		return err
	}
	return nil
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func builtin(id string) Configuration {
	c := Configuration{ID: id, Extends: id, Enabled: true, Env: map[string]string{}, Defaults: map[string]any{}}
	c.Name = defaultName(c)
	return c
}

// List is every configuration: the two built-ins first, with their overrides applied, then the added ones in
// the order of the file. The result is a copy.
func (f *File) List() []Configuration {
	out := []Configuration{builtin(Claude), builtin(Codex)}
	for _, c := range f.items {
		if BuiltIn(c.ID) {
			i := 0
			if c.ID == Codex {
				i = 1
			}
			out[i] = copyOf(c)
			continue
		}
		out = append(out, copyOf(c))
	}
	return out
}

// Get is the configuration with the ID.
func (f *File) Get(id string) (Configuration, bool) {
	for _, c := range f.List() {
		if c.ID == id {
			return c, true
		}
	}
	return Configuration{}, false
}

func copyOf(c Configuration) Configuration {
	env := make(map[string]string, len(c.Env))
	for k, v := range c.Env {
		env[k] = v
	}
	defs := make(map[string]any, len(c.Defaults))
	for k, v := range c.Defaults {
		defs[k] = v
	}
	c.Env, c.Defaults = env, defs
	return c
}

// index is the position of id in f.items, creating the override entry of a built-in when it has none.
func (f *File) index(id string) (int, error) {
	for i, c := range f.items {
		if c.ID == id {
			return i, nil
		}
	}
	if BuiltIn(id) {
		f.items = append(f.items, builtin(id))
		return len(f.items) - 1, nil
	}
	return -1, ErrUnknown
}

// Add appends a configuration. Its ID must be a new slug; an empty name becomes the ID; the environment keeps
// the names and values given (a value may be empty).
func (f *File) Add(c Configuration) error {
	if !ValidID(c.ID) {
		return fmt.Errorf("%w: id must be lower-case letters, digits and hyphens, up to 40", ErrInvalid)
	}
	if BuiltIn(c.ID) {
		return fmt.Errorf("%s: %w", c.ID, ErrExists)
	}
	if _, ok := f.Get(c.ID); ok {
		return fmt.Errorf("%s: %w", c.ID, ErrExists)
	}
	if c.Extends != Claude && c.Extends != Codex {
		return fmt.Errorf("%w: extends must be %s or %s", ErrInvalid, Claude, Codex)
	}
	if err := checkFields(c); err != nil {
		return err
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	c.Enabled = true
	f.items = append(f.items, copyOf(c))
	return nil
}

// Patch is a change of an existing configuration; a nil field is left as it is.
type Patch struct {
	Name    *string
	Command *string
	Enabled *bool
	// Defaults are set one by one over the existing ones.
	Defaults map[string]any
}

// Update applies p. A built-in can change its name, enabled flag and defaults, not its command.
func (f *File) Update(id string, p Patch) error {
	i, err := f.index(id)
	if err != nil {
		return err
	}
	c := copyOf(f.items[i])
	if p.Command != nil {
		if BuiltIn(id) {
			return fmt.Errorf("%s: %w", id, ErrBuiltIn)
		}
		c.Command = *p.Command
	}
	if p.Name != nil {
		if strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("%w: name is empty", ErrInvalid)
		}
		c.Name = strings.TrimSpace(*p.Name)
	}
	if p.Enabled != nil {
		c.Enabled = *p.Enabled
	}
	for k, v := range p.Defaults {
		if k == "" {
			return fmt.Errorf("%w: option id", ErrInvalid)
		}
		c.Defaults[k] = v
	}
	if err := checkFields(c); err != nil {
		return err
	}
	f.items[i] = c
	return nil
}

// SetEnv sets one variable of a configuration. A built-in has no environment.
func (f *File) SetEnv(id, name, value string) error {
	if BuiltIn(id) {
		return fmt.Errorf("%s: %w", id, ErrBuiltIn)
	}
	if !ValidEnvName(name) {
		return fmt.Errorf("%w: environment variable name", ErrInvalid)
	}
	i, err := f.index(id)
	if err != nil {
		return err
	}
	f.items[i].Env[name] = value
	return nil
}

// UnsetEnv removes one variable from a configuration; a variable it does not have is not an error.
func (f *File) UnsetEnv(id, name string) error {
	i, err := f.index(id)
	if err != nil {
		return err
	}
	delete(f.items[i].Env, name)
	return nil
}

// Remove deletes an added configuration; a built-in can only be disabled.
func (f *File) Remove(id string) error {
	if BuiltIn(id) {
		return fmt.Errorf("%s: %w", id, ErrBuiltIn)
	}
	for i, c := range f.items {
		if c.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return ErrUnknown
}

// Stamp identifies the state of the file on disk, cheaply: it changes whenever the file is replaced or edited.
type Stamp struct {
	ModTimeNano int64
	Size        int64
}

// StampOf is the stamp of providers.json in home; a missing file has the zero stamp.
func StampOf(home string) Stamp {
	fi, err := os.Stat(Path(home))
	if err != nil {
		return Stamp{}
	}
	return Stamp{ModTimeNano: fi.ModTime().UnixNano(), Size: fi.Size()}
}

// EnvNames is the variable names of c in order.
func (c Configuration) EnvNames() []string {
	names := make([]string, 0, len(c.Env))
	for n := range c.Env {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// EnvSet says whether the variable has a value for c's processes: set in the configuration, or inherited from
// the agent's environment (environ, as os.Environ() gives it).
func (c Configuration) EnvSet(name string, environ []string) bool {
	if c.Env[name] != "" {
		return true
	}
	prefix := name + "="
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) && len(kv) > len(prefix) {
			return true
		}
	}
	return false
}

// Environ is the environment of c's processes: base (the agent's) without the agent's own HOPLET_* variables,
// with c's variables over it. A variable of c with an empty value is not set here and leaves the agent's.
func (c Configuration) Environ(base []string) []string {
	over := map[string]bool{}
	for n, v := range c.Env {
		if v != "" {
			over[n] = true
		}
	}
	out := make([]string, 0, len(base)+len(over))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, hopletEnvPf) || over[name] {
			continue
		}
		out = append(out, kv)
	}
	for _, n := range c.EnvNames() {
		if c.Env[n] != "" {
			out = append(out, n+"="+c.Env[n])
		}
	}
	return out
}

// SourceDir is the directory that makes c a session source of its own: CLAUDE_CONFIG_DIR for a Claude Code
// configuration, CODEX_HOME for a Codex one; empty when c sets neither.
func (c Configuration) SourceDir() string {
	switch c.Extends {
	case Claude:
		return c.Env["CLAUDE_CONFIG_DIR"]
	case Codex:
		return c.Env["CODEX_HOME"]
	}
	return ""
}

func sortStrings(s []string) { sort.Strings(s) }

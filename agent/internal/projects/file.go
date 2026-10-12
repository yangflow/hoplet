package projects

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

const fileName = "projects.json"

type Item struct {
	Path     string   `json:"path"`
	Imported []string `json:"imported"`
}

type File struct {
	Items []Item
}

type Pending struct {
	SessionID string
	Cwd       string
	Imported  bool
}

func Load(home string) (*File, error) {
	raw, err := os.ReadFile(filepath.Join(home, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Items: []Item{}}, nil
		}
		return nil, fmt.Errorf("read projects: %w", err)
	}
	var probe struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parse projects: %w", err)
	}
	items := make([]Item, 0, len(probe.Projects))
	for i, r := range probe.Projects {
		r = bytes.TrimSpace(r)
		if len(r) == 0 || bytes.Equal(r, []byte("null")) {
			return nil, fmt.Errorf("parse projects: projects[%d] is empty", i)
		}
		if r[0] == '"' {
			return nil, fmt.Errorf("parse projects: projects[%d] must be {\"path\",\"imported\"}, not a string", i)
		}
		var it Item
		if err := json.Unmarshal(r, &it); err != nil {
			return nil, fmt.Errorf("parse projects: projects[%d]: %w", i, err)
		}
		p := Clean(it.Path)
		if p == "" || p == "." {
			return nil, fmt.Errorf("parse projects: projects[%d] missing path", i)
		}
		if it.Imported == nil {
			it.Imported = []string{}
		}
		it.Path = p
		items = append(items, it)
	}
	return &File{Items: items}, nil
}

func (f *File) Save(home string) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(struct {
		Projects []Item `json:"projects"`
	}{Projects: f.Items}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, fileName), append(raw, '\n'), 0o600)
}

func (f *File) Sync(sources []provider.Source) []provider.Source {
	type project struct {
		path, real string
		set        map[string]struct{}
	}
	var want []project
	for _, it := range f.Items {
		set := map[string]struct{}{}
		for _, id := range it.Imported {
			set[id] = struct{}{}
		}
		want = append(want, project{path: Clean(it.Path), real: Real(it.Path), set: set})
	}
	var out []provider.Source
	for _, src := range sources {
		for _, w := range want {
			if !inProject(src, w.path, w.real) {
				continue
			}
			if _, ok := w.set[src.ID()]; ok {
				out = append(out, src)
				break
			}
		}
	}
	return out
}

func (f *File) Suggest(sources []provider.Source) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, src := range sources {
		p := Clean(CwdOf(src))
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func (f *File) Sessions(path string, sources []provider.Source) []Pending {
	p, added := Real(path), Clean(path)
	imported := map[string]struct{}{}
	for _, it := range f.Items {
		if Real(it.Path) != p {
			continue
		}
		for _, id := range it.Imported {
			imported[id] = struct{}{}
		}
	}
	var out []Pending
	seen := map[string]struct{}{}
	for _, src := range sources {
		if !inProject(src, added, p) {
			continue
		}
		id := src.ID()
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		_, ok := imported[id]
		out = append(out, Pending{
			SessionID: id,
			Cwd:       p,
			Imported:  ok,
		})
	}
	return out
}

func (f *File) Add(path string) error {
	p := Clean(path)
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute")
	}
	for _, it := range f.Items {
		if Real(it.Path) == Real(p) {
			return nil
		}
	}
	f.Items = append(f.Items, Item{Path: p, Imported: []string{}})
	return nil
}

func (f *File) Import(path string, ids []string) error {
	for _, id := range ids {
		if !provider.ValidSessionID(id) {
			return fmt.Errorf("invalid-session-id")
		}
	}
	p := Real(path)
	idx := -1
	for i, it := range f.Items {
		if Real(it.Path) == p {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown project")
	}
	have := map[string]struct{}{}
	for _, id := range f.Items[idx].Imported {
		have[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := have[id]; ok {
			continue
		}
		f.Items[idx].Imported = append(f.Items[idx].Imported, id)
		have[id] = struct{}{}
	}
	return nil
}

// RemovePath drops a watched project directory from the allowlist.
func (f *File) RemovePath(path string) error {
	p := Real(path)
	for i, it := range f.Items {
		if Real(it.Path) == p {
			f.Items = append(f.Items[:i], f.Items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("unknown project")
}

// ClearImports drops imported session ids from a watched path. ids must be non-empty.
func (f *File) ClearImports(path string, ids []string) error {
	for _, id := range ids {
		if !provider.ValidSessionID(id) {
			return fmt.Errorf("invalid-session-id")
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("missing session ids")
	}
	p := Real(path)
	idx := -1
	for i, it := range f.Items {
		if Real(it.Path) == p {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown project")
	}
	drop := map[string]struct{}{}
	for _, id := range ids {
		drop[id] = struct{}{}
	}
	if len(drop) == 0 {
		return fmt.Errorf("missing session ids")
	}
	kept := make([]string, 0, len(f.Items[idx].Imported))
	for _, id := range f.Items[idx].Imported {
		if _, ok := drop[id]; ok {
			continue
		}
		kept = append(kept, id)
	}
	f.Items[idx].Imported = kept
	return nil
}

// CwdOf is the directory the session runs in, trimmed.
func CwdOf(src provider.Source) string {
	return strings.TrimSpace(src.Cwd())
}

func Clean(p string) string {
	return filepath.Clean(strings.TrimSpace(p))
}

// inProject is whether src belongs to the project at path as added or at its real path: Claude Code records
// the real one.
func inProject(src provider.Source, path, real string) bool {
	return src.InProject(real) || path != real && src.InProject(path)
}

// Real is Clean(p) with symbolic links resolved: the path a session run there records. A path that cannot be
// resolved stays as it is cleaned.
func Real(p string) string {
	c := Clean(p)
	if r, err := filepath.EvalSymlinks(c); err == nil {
		return r
	}
	return c
}

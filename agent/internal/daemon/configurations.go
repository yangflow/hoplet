package daemon

import (
	"path/filepath"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/configs"
	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
	codexprovider "github.com/yangflow/hoplet/agent/internal/provider/codex"
)

// extraSource is the session store of a configuration whose environment sets CLAUDE_CONFIG_DIR or CODEX_HOME
// (ADR 0028): it is scanned like the vendor's own, and its sessions belong to that configuration.
type extraSource struct {
	key  string // extends and store root
	conf string // the first configuration that names the store
	p    provider.Provider
}

// storeRoot is where conf keeps its sessions when it has a directory of its own, and whether it has one that
// differs from the vendor's own store.
func storeRoot(cfg *config.Config, conf configs.Configuration) (string, bool) {
	dir := conf.SourceDir()
	if dir == "" {
		return "", false
	}
	var root, own string
	switch conf.Extends {
	case configs.Claude:
		root, own = filepath.Join(dir, "projects"), cfg.ClaudeProjects
	case configs.Codex:
		root, own = codexprovider.SessionsDir(dir), codexprovider.SessionsDir(cfg.CodexHome)
	default:
		return "", false
	}
	if filepath.Clean(root) == filepath.Clean(own) {
		return "", false
	}
	return root, true
}

// setSources makes the session stores of confs the extra sources: stores kept from before keep their scan
// state, those no configuration names any longer are dropped.
func setSources(cfg *config.Config, confs []configs.Configuration) {
	vendorMu.Lock()
	defer vendorMu.Unlock()
	vendorsLocked(cfg)
	old := map[string]*extraSource{}
	for _, e := range extras {
		old[e.key] = e
	}
	var next []*extraSource
	seen := map[string]bool{}
	for _, c := range confs {
		root, ok := storeRoot(cfg, c)
		if !ok {
			continue
		}
		key := c.Extends + "\x00" + filepath.Clean(root)
		if seen[key] {
			continue
		}
		seen[key] = true
		if e, ok := old[key]; ok {
			e.conf = c.ID
			next = append(next, e)
			continue
		}
		var p provider.Provider
		if c.Extends == configs.Claude {
			cl := claudeprovider.New(root, cfg.ExcludePrefixes)
			cl.SetProjectHints(func() []string { return watchedPaths(cfg.HopletHome) })
			p = cl
		} else {
			p = codexprovider.New(root, cfg.ExcludePrefixes)
		}
		next = append(next, &extraSource{key: key, conf: c.ID, p: p})
	}
	extras = next
}

// extraConfiguration is the configuration that owns p, when p is the store of one.
func extraConfiguration(p provider.Provider) (string, bool) {
	vendorMu.Lock()
	defer vendorMu.Unlock()
	for _, e := range extras {
		if e.p == p {
			return e.conf, true
		}
	}
	return "", false
}

func setScanOwners(m map[string]string) {
	vendorMu.Lock()
	scanOwners = m
	vendorMu.Unlock()
}

// extraOwner is the configuration whose own store holds the session, as the last scan found it.
func extraOwner(sessionID string) (string, bool) {
	vendorMu.Lock()
	defer vendorMu.Unlock()
	id, ok := scanOwners[sessionID]
	return id, ok
}

// storeOf is the provider that holds the sessions of conf: its own store, or its vendor's.
func storeOf(cfg *config.Config, conf configs.Configuration) provider.Provider {
	vendorMu.Lock()
	defer vendorMu.Unlock()
	all := vendorsLocked(cfg)
	if root, ok := storeRoot(cfg, conf); ok {
		key := conf.Extends + "\x00" + filepath.Clean(root)
		for _, e := range extras {
			if e.key == key {
				return e.p
			}
		}
	}
	for _, v := range all {
		if v.Name() == conf.Extends {
			return v
		}
	}
	return nil
}

// ownerByFind is the configuration of a session no record names: the one whose store holds it, else the
// built-in of the vendor whose store does; empty when no store has it.
func ownerByFind(cfg *config.Config, sessionID string) string {
	if id, ok := extraOwner(sessionID); ok {
		return id
	}
	vendorMu.Lock()
	all := vendorsLocked(cfg)
	vendorMu.Unlock()
	for _, v := range all {
		if _, ok := v.Find(sessionID); ok {
			if id, ok := extraConfiguration(v); ok {
				return id
			}
			return v.Name()
		}
	}
	return ""
}

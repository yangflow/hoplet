// Package codex is the provider of Codex (ADR 0012, ADR 0027): it serves sessions from the append-only rollout
// files Codex writes under $CODEX_HOME/sessions. A rollout is `rollout-<time>-<threadId>.jsonl`, or the same
// bytes compressed in place to `.jsonl.zst`; the sync cursor counts the uncompressed bytes in both (ADR 0002).
// The provider is also the Store of Codex: handoff installs a rollout under sessions/ (store.go).
package codex

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// Provider serves one source per Codex thread. Sources outlive scans: the ID and cwd on a rollout's first
// line never change, so a source reads them once. The state is disposable; a fresh Provider rebuilds it.
type Provider struct {
	root    string // $CODEX_HOME/sessions
	exclude []string

	mu      sync.Mutex
	sources map[string]*source // by rollout path without the .zst suffix; only what the latest scan saw
}

// New serves the sessions under root, Codex's sessions directory, except those running in a directory under
// one of the exclude prefixes.
func New(root string, exclude []string) *Provider {
	return &Provider{root: root, exclude: exclude}
}

var _ provider.Provider = (*Provider)(nil)

// Name is the provider value on the wire.
const Name = "codex"

func (p *Provider) Name() string { return Name }

func (p *Provider) Scan(_ context.Context) ([]provider.Source, error) {
	files := scanFiles(p.root)
	p.mu.Lock()
	defer p.mu.Unlock()
	next := make(map[string]*source, len(files))
	out := make([]provider.Source, 0, len(files))
	for _, f := range files {
		s := p.sources[f.base]
		if s == nil {
			s = &source{file: f}
		} else {
			s.update(f)
		}
		if !s.ready() {
			continue
		}
		next[f.base] = s
		if pathExcluded(s.Cwd(), p.exclude) {
			continue
		}
		out = append(out, s)
	}
	for base, old := range p.sources {
		if next[base] == nil {
			old.close()
		}
	}
	p.sources = next
	return out, nil
}

// Find locates the session's rollout.
func (p *Provider) Find(sessionID string) (provider.Found, bool) {
	if !provider.ValidSessionID(sessionID) {
		return provider.Found{}, false
	}
	for _, f := range scanFiles(p.root) {
		if f.key != sessionID {
			continue
		}
		s := &source{file: f}
		if !s.ready() || s.ID() != sessionID {
			continue
		}
		return provider.Found{Cwd: s.Cwd(), NativePath: f.current()}, true
	}
	return provider.Found{}, false
}

func pathExcluded(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && path != "" && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// SessionsDir is the sessions directory of the Codex home codexHome.
func SessionsDir(codexHome string) string { return filepath.Join(codexHome, "sessions") }

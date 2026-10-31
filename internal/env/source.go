package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/config"
)

// Snapshot is a resolved harness environment.
type Snapshot struct {
	Shell string `json:"shell"`
	// Vars is the environment, KEY=VALUE, sorted.
	Vars []string `json:"vars"`
	// Dropped are the names of credential variables withheld.
	Dropped []string `json:"dropped,omitempty"`
	// Proxy is the effective proxy URL with any password redacted; empty
	// for none.
	Proxy       string      `json:"proxy,omitempty"`
	ProxySource ProxySource `json:"proxy_source"`
	ResolvedAt  time.Time   `json:"resolved_at"`
}

// Source provides the harness environment; refresh asks for the login
// shell to be read again.
type Source interface {
	Environment(ctx context.Context, refresh bool) (Snapshot, error)
}

// Static is a Source with a fixed environment (tests, and harnesses run
// without a login shell).
type Static []string

// Environment returns the fixed environment.
func (s Static) Environment(context.Context, bool) (Snapshot, error) {
	return Snapshot{Vars: slices.Clone(s), ProxySource: ProxyNone}, nil
}

// CacheFile is the file in the home directory that keeps the login-shell
// environment between daemon starts. It never holds withheld credentials.
const CacheFile = "env.json"

// Login is the Source of a real daemon: the login shell's environment,
// read once and kept in the home's cache file, with the proxy of the
// configuration, the login shell or the system applied on every call, so
// a configuration change takes effect at the next daemon start.
type Login struct {
	Home string // Hoplet's home directory (cache and configuration)
	// UserHome is the user's home directory the shell starts in.
	UserHome string
	Shell    string
	// SystemProxy reads the system proxy; nil means ReadSystemProxy.
	SystemProxy func(context.Context) (SystemProxy, error)

	mu sync.Mutex
}

// cached is the cache file's content: the filtered login environment.
type cached struct {
	Shell      string            `json:"shell"`
	Vars       map[string]string `json:"vars"`
	Dropped    []string          `json:"dropped,omitempty"`
	ResolvedAt time.Time         `json:"resolved_at"`
}

// Environment returns the harness environment, reading the login shell
// when refresh is set or nothing is cached.
func (l *Login) Environment(ctx context.Context, refresh bool) (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cfg, err := config.Load(l.Home)
	if err != nil {
		return Snapshot{}, err
	}
	c, ok, err := l.load()
	var raw map[string]string // the shell's output, when read in this call
	if err != nil || !ok || refresh || c.Shell != l.Shell {
		if raw, err = login(ctx, l.Shell, l.UserHome); err != nil {
			return Snapshot{}, err
		}
		c = cached{Shell: l.Shell, ResolvedAt: time.Now().UTC().Truncate(time.Second)}
		c.Vars, c.Dropped = filter(raw, nil)
		if err := l.save(c); err != nil {
			return Snapshot{}, err
		}
	}
	// Allowed credentials are never cached: the shell is read again for
	// them whenever the configuration allows any.
	if len(cfg.Env.Allow) > 0 {
		if raw == nil {
			if raw, err = login(ctx, l.Shell, l.UserHome); err != nil {
				return Snapshot{}, err
			}
		}
		c.Vars, c.Dropped = filter(raw, cfg.Env.Allow)
	}
	read := l.SystemProxy
	if read == nil {
		read = ReadSystemProxy
	}
	return build(ctx, c, cfg, read)
}

// build applies the proxy and makes the snapshot.
func build(ctx context.Context, c cached, cfg config.Config, systemProxy func(context.Context) (SystemProxy, error)) (Snapshot, error) {
	vars := map[string]string{}
	for k, v := range c.Vars {
		vars[k] = v
	}
	noProxy := append(slices.Clone(defaultNoProxy), cfg.Network.NoProxy...)
	var src ProxySource
	var proxy string
	switch p := cfg.Network.Proxy; {
	case p == config.ProxyDirect:
		src = ProxyFromConfig
		setProxy(vars, "", nil)
	case p != "":
		src, proxy = ProxyFromConfig, p
		setProxy(vars, p, noProxy)
	case shellProxy(vars) != "":
		src, proxy = ProxyFromShell, shellProxy(vars)
	default:
		sp, err := systemProxy(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("env: read the system proxy: %w", err)
		}
		src, proxy = ProxyFromSystem, sp.URL
		if sp.URL == "" {
			src = ProxyNone
		}
		setProxy(vars, sp.URL, append(noProxy, sp.NoProxy...))
	}
	snap := Snapshot{Shell: c.Shell, Dropped: c.Dropped, Proxy: redact(proxy), ProxySource: src, ResolvedAt: c.ResolvedAt}
	for k, v := range vars {
		snap.Vars = append(snap.Vars, k+"="+v)
	}
	sort.Strings(snap.Vars)
	return snap, nil
}

func (l *Login) path() string { return filepath.Join(l.Home, CacheFile) }

func (l *Login) load() (cached, bool, error) {
	b, err := fs.ReadFile(os.DirFS(l.Home), CacheFile)
	if errors.Is(err, fs.ErrNotExist) {
		return cached{}, false, nil
	}
	if err != nil {
		return cached{}, false, fmt.Errorf("env: read cache: %w", err)
	}
	var c cached
	if err := json.Unmarshal(b, &c); err != nil {
		return cached{}, false, nil // unreadable: read the shell again
	}
	return c, true, nil
}

func (l *Login) save(c cached) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("env: encode cache: %w", err)
	}
	tmp, err := os.CreateTemp(l.Home, ".env-*")
	if err != nil {
		return fmt.Errorf("env: write cache: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("env: write cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), l.path()); err != nil {
		return fmt.Errorf("env: write cache: %w", err)
	}
	return nil
}

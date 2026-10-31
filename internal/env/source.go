package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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
	// Note says why no proxy is in use when that may surprise (the system
	// proxy is a PAC file).
	Note string `json:"note,omitempty"`
	// Stale says why the shell could not be read again when the snapshot
	// is the cached environment without the secrets only the shell has
	// (allowed credentials, proxies with a password); empty otherwise.
	Stale      string    `json:"stale,omitempty"`
	ResolvedAt time.Time `json:"resolved_at"`
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
// a configuration change takes effect at the next daemon start or
// `hoplet env refresh`.
//
// Secrets never reach the cache file: withheld credentials, and proxy
// variables whose value carries a password. The last raw read of the shell
// is kept in memory for the Login's lifetime instead, so allowed
// credentials and such proxies are available after the first read of a
// process without reading the shell again; a cache that lacks secrets
// (NeedsShell), or a configuration that allows credentials, makes the
// first Environment call of a process read the shell once.
type Login struct {
	Home string // Hoplet's home directory (cache and configuration)
	// UserHome is the user's home directory the shell starts in.
	UserHome string
	Shell    string
	// SystemProxy reads the system proxy; nil means ReadSystemProxy.
	SystemProxy func(context.Context) (SystemProxy, error)

	mu  sync.Mutex
	raw map[string]string // the last read of the shell, memory only
}

// cached is the cache file's content: the filtered login environment.
type cached struct {
	Shell   string            `json:"shell"`
	Vars    map[string]string `json:"vars"`
	Dropped []string          `json:"dropped,omitempty"`
	// NeedsShell is set when secrets were left out of Vars, so a process
	// that wants them has to read the shell.
	NeedsShell bool      `json:"needs_shell,omitempty"`
	ResolvedAt time.Time `json:"resolved_at"`
}

// Environment returns the harness environment, reading the login shell
// when refresh is set, nothing usable is cached, the shell changed, or the
// secrets that only the shell has are needed and not yet in memory.
func (l *Login) Environment(ctx context.Context, refresh bool) (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cfg, err := config.Load(l.Home)
	if err != nil {
		return Snapshot{}, err
	}
	c, ok, err := l.load()
	forced := err != nil || !ok || refresh || c.Shell != l.Shell
	var stale string
	if forced || (l.raw == nil && (c.NeedsShell || len(cfg.Env.Allow) > 0)) {
		raw, rerr := login(ctx, l.Shell, l.UserHome)
		switch {
		case rerr == nil:
			l.raw = raw
			c = cached{Shell: l.Shell, ResolvedAt: time.Now().UTC().Truncate(time.Second)}
			c.Vars, c.Dropped = filter(raw, nil)
			c.NeedsShell = stripSecrets(c.Vars)
			if err := l.save(c); err != nil {
				// What was read is good; only the next process loses
				// the cache.
				stale = fmt.Sprintf("could not write the environment cache: %v", err)
			}
		case forced:
			return Snapshot{}, rerr
		default:
			// Only the secrets needed the shell: keep the cached
			// environment without them and say so.
			stale = fmt.Sprintf("could not read %s again (%v); using the environment cached at %s",
				l.Shell, rerr, c.ResolvedAt.Format(time.RFC3339))
		}
	}
	vars := c.Vars
	if l.raw != nil {
		vars, c.Dropped = filter(l.raw, cfg.Env.Allow)
	}
	systemProxy := l.SystemProxy
	if systemProxy == nil {
		systemProxy = ReadSystemProxy
	}
	snap, err := build(ctx, c, vars, cfg, systemProxy)
	snap.Stale = stale
	return snap, err
}

// build applies the proxy and makes the snapshot.
func build(ctx context.Context, c cached, cachedVars map[string]string, cfg config.Config, systemProxy func(context.Context) (SystemProxy, error)) (Snapshot, error) {
	vars := maps.Clone(cachedVars)
	noProxy := mergeNoProxy(defaultNoProxy, cfg.Network.NoProxy)
	var src ProxySource
	var proxy, note string
	switch p := cfg.Network.Proxy; {
	case p == config.ProxyDirect:
		src = ProxyFromConfig
		setProxy(vars, "", nil)
	case p != "":
		src, proxy = ProxyFromConfig, p
		setProxy(vars, p, noProxy)
	case shellProxy(vars) != "":
		src, proxy = ProxyFromShell, shellProxy(vars)
		setProxy(vars, proxy, mergeNoProxy(shellNoProxy(vars), noProxy))
	default:
		sp, err := systemProxy(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("env: read the system proxy: %w", err)
		}
		src, proxy = ProxyFromSystem, sp.URL
		if sp.URL == "" {
			src = ProxyNone
			if sp.PAC != "" {
				note = "the system proxy is a PAC file (" + RedactProxy(sp.PAC) + "); set network.proxy in config.toml"
			}
		}
		setProxy(vars, sp.URL, mergeNoProxy(noProxy, sp.NoProxy))
	}
	snap := Snapshot{Shell: c.Shell, Dropped: c.Dropped, Proxy: RedactProxy(proxy), ProxySource: src, Note: note, ResolvedAt: c.ResolvedAt}
	for k, v := range vars {
		snap.Vars = append(snap.Vars, k+"="+v)
	}
	sort.Strings(snap.Vars)
	return snap, nil
}

// mergeNoProxy joins no-proxy lists, dropping duplicates and keeping order.
func mergeNoProxy(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, e := range l {
			if e = strings.TrimSpace(e); e != "" && !slices.Contains(out, e) {
				out = append(out, e)
			}
		}
	}
	return out
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
	// The file may come from an older build or from someone's hands: filter
	// it as a fresh read would be.
	kept, dropped := filter(c.Vars, nil)
	if stripSecrets(kept) {
		c.NeedsShell = true
	}
	c.Vars = kept
	c.Dropped = append(c.Dropped, dropped...)
	slices.Sort(c.Dropped)
	c.Dropped = slices.Compact(c.Dropped)
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

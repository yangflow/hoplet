// Package config reads Hoplet's configuration file, config.toml in the home
// directory. A missing file means the defaults.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the configuration file inside the home directory.
const FileName = "config.toml"

// Config is the whole configuration.
type Config struct {
	Network Network `toml:"network"`
	Env     Env     `toml:"env"`
}

// Network configures outbound connections of the daemon and its harnesses.
type Network struct {
	// Proxy is the proxy URL (http://, https://, socks5://, socks5h://).
	// Empty means: the proxy the login shell exports, else the macOS system
	// proxy. "direct" means no proxy at all.
	Proxy string `toml:"proxy"`
	// NoProxy lists hosts reached without the proxy, added to the defaults
	// (localhost and loopback addresses).
	NoProxy []string `toml:"no_proxy"`
}

// Env configures the environment of harness processes.
type Env struct {
	// Allow names variables passed to harnesses although their names look
	// like credentials.
	Allow []string `toml:"allow"`
}

// ProxyDirect is the Network.Proxy value that disables proxying.
const ProxyDirect = "direct"

// Path is the configuration file of a home directory.
func Path(home string) string { return filepath.Join(home, FileName) }

// Load reads home's configuration file. A missing file yields the zero
// Config; unknown keys are errors, so a typo never silently does nothing.
func Load(home string) (Config, error) {
	var c Config
	b, err := fs.ReadFile(os.DirFS(home), FileName)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	md, err := toml.Decode(string(b), &c)
	if err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", Path(home), err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("config: %s: unknown keys %s", Path(home), strings.Join(keys, ", "))
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", Path(home), err)
	}
	return c, nil
}

func (c Config) validate() error {
	p := c.Network.Proxy
	if p == "" || p == ProxyDirect {
		return nil
	}
	for _, scheme := range []string{"http://", "https://", "socks5://", "socks5h://"} {
		if strings.HasPrefix(p, scheme) {
			return nil
		}
	}
	return fmt.Errorf("network.proxy %q: want an http, https, socks5 or socks5h URL, or %q", p, ProxyDirect)
}

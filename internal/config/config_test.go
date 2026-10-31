package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	h := t.TempDir()
	if err := os.WriteFile(filepath.Join(h, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil || !reflect.DeepEqual(c, Config{}) {
		t.Fatalf("load %+v %v", c, err)
	}
}

func TestLoad(t *testing.T) {
	h := write(t, `
# comments are fine
[network]
proxy = "socks5h://127.0.0.1:7890"
no_proxy = [".corp", "10.0.0.0/8"]

[env]
allow = ["GH_TOKEN"]
`)
	c, err := Load(h)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Network: Network{Proxy: "socks5h://127.0.0.1:7890", NoProxy: []string{".corp", "10.0.0.0/8"}},
		Env:     Env{Allow: []string{"GH_TOKEN"}},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, tc := range map[string]struct{ body, inErr string }{
		"unknown key":   {"[network]\nproxi = \"x\"\n", "unknown keys network.proxi"},
		"bad syntax":    {"[network\n", "config.toml"},
		"bad scheme":    {"[network]\nproxy = \"ftp://x\"\n", "network.proxy"},
		"wrong type":    {"[env]\nallow = \"GH_TOKEN\"\n", "config.toml"},
		"unknown table": {"[nope]\nx = 1\n", "unknown keys nope"},
	} {
		_, err := Load(write(t, tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.inErr) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.inErr)
		}
	}
	if _, err := Load(write(t, "[network]\nproxy = \"direct\"\n")); err != nil {
		t.Errorf("direct: %v", err)
	}
}

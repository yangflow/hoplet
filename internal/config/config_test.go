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

func TestHarnessFollow(t *testing.T) {
	for body, want := range map[string]bool{
		"":                                   true,
		"[harness.claude]\nfollow = true\n":  true,
		"[harness.claude]\nfollow = false\n": false,
	} {
		c, err := Load(write(t, body))
		if err != nil || c.Harness.Claude.Follows() != want {
			t.Errorf("%q: follows %v %v, want %v", body, c.Harness.Claude.Follows(), err, want)
		}
	}
}

func TestLoadRejects(t *testing.T) {
	for name, tc := range map[string]struct{ body, inErr string }{
		"unknown key":     {"[network]\nproxi = \"x\"\n", "unknown keys network.proxi"},
		"bad syntax":      {"[network\n", "config.toml"},
		"bad scheme":      {"[network]\nproxy = \"ftp://x\"\n", "network.proxy"},
		"wrong type":      {"[env]\nallow = \"GH_TOKEN\"\n", "config.toml"},
		"unknown table":   {"[nope]\nx = 1\n", "unknown keys nope"},
		"no scheme":       {"[network]\nproxy = \"alice:hunter2@h:1\"\n", "missing scheme"},
		"no host":         {"[network]\nproxy = \"http://\"\n", "no host"},
		"empty no_proxy":  {"[network]\nno_proxy = [\"\"]\n", "no_proxy"},
		"spaced no_proxy": {"[network]\nno_proxy = [\"a b\"]\n", "no_proxy"},
		"comma no_proxy":  {"[network]\nno_proxy = [\"a,b\"]\n", "no_proxy"},
		"unknown harness": {"[harness.codex]\nfollow = false\n", "unknown keys harness.codex"},
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

func TestLoadDoesNotEchoPasswords(t *testing.T) {
	for _, body := range []string{
		"[network]\nproxy = \"ftp://u:hunter2@h:1\"\n",
		"[network]\nproxy = \"http://u:hunter2@\"\n",
		"[network]\nproxy = \"http://u:hunter2@h:bad\"\n",
	} {
		_, err := Load(write(t, body))
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: %v", body, err)
		}
	}
}

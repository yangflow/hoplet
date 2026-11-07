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
		if err != nil || c.Harness.Claude.Follows() != want || !c.Harness.Hermes.Follows() {
			t.Errorf("%q: follows %v %v, want %v", body, c.Harness.Claude.Follows(), err, want)
		}
	}
	c, err := Load(write(t, "[harness.hermes]\nfollow = false\n"))
	if err != nil || c.Harness.Hermes.Follows() || !c.Harness.Claude.Follows() {
		t.Fatalf("hermes off: %+v %v", c.Harness, err)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, tc := range map[string]struct{ body, inErr string }{
		"unknown key":       {"[network]\nproxi = \"x\"\n", "unknown keys network.proxi"},
		"bad syntax":        {"[network\n", "config.toml"},
		"bad scheme":        {"[network]\nproxy = \"ftp://x\"\n", "network.proxy"},
		"wrong type":        {"[env]\nallow = \"GH_TOKEN\"\n", "config.toml"},
		"unknown table":     {"[nope]\nx = 1\n", "unknown keys nope"},
		"no scheme":         {"[network]\nproxy = \"alice:hunter2@h:1\"\n", "missing scheme"},
		"no host":           {"[network]\nproxy = \"http://\"\n", "no host"},
		"empty no_proxy":    {"[network]\nno_proxy = [\"\"]\n", "no_proxy"},
		"spaced no_proxy":   {"[network]\nno_proxy = [\"a b\"]\n", "no_proxy"},
		"comma no_proxy":    {"[network]\nno_proxy = [\"a,b\"]\n", "no_proxy"},
		"unknown harness":   {"[harness.codex]\nfollow = false\n", "unknown keys harness.codex"},
		"listen no port":    {"[network]\nlisten = \"0.0.0.0\"\n", "network.listen"},
		"listen empty port": {"[network]\nlisten = \"0.0.0.0:\"\n", "network.listen"},
		"route scheme":      {"[network]\nroutes = [\"http://h:1\"]\n", "network.routes"},
		"route no host":     {"[network]\nroutes = [\"ws://:1\"]\n", "network.routes"},
		"route path":        {"[network]\nroutes = [\"ws://h:1/v1/noise\"]\n", "network.routes"},
		"route query":       {"[network]\nroutes = [\"wss://h?x=1\"]\n", "network.routes"},
		"route user":        {"[network]\nroutes = [\"wss://u@h\"]\n", "network.routes"},
		"route bare":        {"[network]\nroutes = [\"h:1\"]\n", "network.routes"},
	} {
		_, err := Load(write(t, tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.inErr) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.inErr)
		}
	}
	if _, err := Load(write(t, "[network]\nproxy = \"direct\"\n")); err != nil {
		t.Errorf("direct: %v", err)
	}
	c, err := Load(write(t, "[network]\nlisten = \"0.0.0.0:7443\"\nroutes = [\"ws://192.0.2.1:7443\", \"wss://b.example\", \"ws://[2001:db8::1]:7443\"]\n"))
	if err != nil || c.Network.Listen != "0.0.0.0:7443" || len(c.Network.Routes) != 3 {
		t.Errorf("listen and routes: %+v %v", c.Network, err)
	}
	c, err = Load(write(t, "[network]\nrelays = [\"wss://relay.example\", \"wss://backup.example:8443\"]\n"))
	if err != nil || len(c.Network.Relays) != 2 {
		t.Errorf("relays: %+v %v", c.Network, err)
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

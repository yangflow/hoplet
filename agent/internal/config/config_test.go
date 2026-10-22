package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadClaudeProjectsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"server_url":"http://127.0.0.1:1","token":"t","claude_projects":"/tmp/claude-new"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClaudeProjects != "/tmp/claude-new" {
		t.Fatalf("got %q", cfg.ClaudeProjects)
	}
}

func TestLoadCodexHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ value, env, want string }{
		{"", "", filepath.Join(home, ".codex")},
		{"", "/srv/codex-env", "/srv/codex-env"},
		{"~/.codex-alt", "/srv/codex-env", filepath.Join(home, ".codex-alt")},
		{"/srv/codex", "", "/srv/codex"},
	} {
		t.Setenv("CODEX_HOME", tc.env)
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t","codex_home":"` + tc.value + `"}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CodexHome != tc.want {
			t.Fatalf("codex_home %q with CODEX_HOME %q = %q, want %q", tc.value, tc.env, cfg.CodexHome, tc.want)
		}
	}
}

func TestLoadExplicitHopletHome(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	path := filepath.Join(dir, "config.json")
	raw := `{"server_url":"http://127.0.0.1:1","token":"t","hoplet_home":"` + state + `","claude_projects":"` + filepath.Join(dir, "claude") + `"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HopletHome != state {
		t.Fatalf("state root = %q, want %q", cfg.HopletHome, state)
	}
}

func TestLoadBrowseRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ value, want string }{
		{"", home}, {"~", home}, {"~/code", filepath.Join(home, "code")}, {"/srv/work", "/srv/work"},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t","browse_root":"` + tc.value + `"}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BrowseRoot != tc.want {
			t.Fatalf("browse_root %q = %q, want %q", tc.value, cfg.BrowseRoot, tc.want)
		}
	}
}

func TestLoadHopletHomeDefaultsAndExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ value, want string }{
		{"", DefaultHome()}, {"~", home}, {"~/.hoplet-test", filepath.Join(home, ".hoplet-test")},
	} {
		t.Run(tc.value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			raw := `{"server_url":"http://127.0.0.1:1","token":"t","hoplet_home":"` + tc.value + `"}`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.HopletHome != tc.want {
				t.Fatalf("got %q, want %q", cfg.HopletHome, tc.want)
			}
		})
	}
}

func TestLoadRuntimeSettings(t *testing.T) {
	load := func(extra string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t"` + extra + `}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	cfg, err := load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClaudeBin != "claude" || cfg.CodexBin != "codex" || cfg.RuntimeIdleMinutes != 30 || cfg.RuntimeMaxIdleProcesses != 3 {
		t.Fatalf("defaults: %+v", cfg)
	}
	cfg, err = load(`,"claude_bin":"/opt/claude","codex_bin":"/opt/codex","runtime_idle_minutes":5,"runtime_max_idle_processes":0`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClaudeBin != "/opt/claude" || cfg.CodexBin != "/opt/codex" || cfg.RuntimeIdleMinutes != 5 || cfg.RuntimeMaxIdleProcesses != 0 {
		t.Fatalf("explicit: %+v", cfg)
	}
	if _, err := load(`,"runtime_idle_minutes":0`); err == nil {
		t.Fatal("zero idle minutes accepted")
	}
}

func TestLoadTerminalSetting(t *testing.T) {
	load := func(extra string) *Config {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t"` + extra + `}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if load("").Terminal {
		t.Fatal("the terminal is on by default")
	}
	if !load(`,"terminal":true`).Terminal {
		t.Fatal("terminal:true is ignored")
	}
}

func TestLoadSessionMCPSetting(t *testing.T) {
	load := func(extra string) *Config {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t"` + extra + `}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if load("").SessionMCP {
		t.Fatal("the session MCP server is on by default")
	}
	if !load(`,"session_mcp":true`).SessionMCP {
		t.Fatal("session_mcp:true is ignored")
	}
}

func TestLoadPreviewHost(t *testing.T) {
	load := func(extra string) *Config {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t"` + extra + `}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	host, _ := os.Hostname()
	if got := load("").PreviewHost; got != host {
		t.Fatalf("default preview_host = %q, want the host name %q", got, host)
	}
	if got := load(`,"preview_host":" box.tail.ts.net "`).PreviewHost; got != "box.tail.ts.net" {
		t.Fatalf("preview_host = %q", got)
	}
}

func TestLoadPermissionDeny(t *testing.T) {
	load := func(extra string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t"` + extra + `}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	cfg, err := load(`,"permission_deny":[{"id":"local.no-curl","tool":"run","match":{"command":"curl"},"action":"deny","note":"no curl"}]`)
	if err != nil || len(cfg.PermissionDeny) != 1 || cfg.PermissionDeny[0].ID != "local.no-curl" {
		t.Fatalf("deny rule not loaded: %+v %v", cfg, err)
	}
	for name, extra := range map[string]string{
		"allow":     `,"permission_deny":[{"id":"x","tool":"run","match":{},"action":"allow"}]`,
		"no id":     `,"permission_deny":[{"tool":"run","match":{},"action":"deny"}]`,
		"bad match": `,"permission_deny":[{"id":"x","tool":"run","match":{"paths":["/a"]},"action":"deny"}]`,
		"duplicate": `,"permission_deny":[{"id":"x","tool":"run","match":{},"action":"deny"},{"id":"x","tool":"run","match":{},"action":"deny"}]`,
	} {
		if _, err := load(extra); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

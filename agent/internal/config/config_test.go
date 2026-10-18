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
	if cfg.ClaudeBin != "claude" || cfg.RuntimeIdleMinutes != 30 || cfg.RuntimeMaxIdleProcesses != 3 {
		t.Fatalf("defaults: %+v", cfg)
	}
	cfg, err = load(`,"claude_bin":"/opt/claude","runtime_idle_minutes":5,"runtime_max_idle_processes":0`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClaudeBin != "/opt/claude" || cfg.RuntimeIdleMinutes != 5 || cfg.RuntimeMaxIdleProcesses != 0 {
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

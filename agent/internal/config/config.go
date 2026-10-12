package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ServerURL       string        `json:"server_url"`
	Token           string        `json:"token"`
	ClaudeProjects  string        `json:"claude_projects"`
	DeviceName      string        `json:"device_name"`
	DeviceID        string        `json:"device_id,omitempty"`
	PollInterval    time.Duration `json:"-"`
	PollIntervalMS  int           `json:"poll_interval_ms"`
	ExcludePrefixes []string      `json:"exclude_prefixes"`
	HopletHome      string        `json:"hoplet_home"`
	BrowseRoot      string        `json:"browse_root"`

	ClaudeBin               string `json:"claude_bin"`
	RuntimeIdleMinutes      int    `json:"runtime_idle_minutes"`
	RuntimeMaxIdleProcesses int    `json:"runtime_max_idle_processes"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		PollIntervalMS:  1000,
		ExcludePrefixes: []string{"/private/var/folders", "/var/folders", "/tmp/"},
		HopletHome:      DefaultHome(),

		ClaudeBin:               "claude",
		RuntimeIdleMinutes:      30,
		RuntimeMaxIdleProcesses: 3,
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.HopletHome == "" {
		cfg.HopletHome = DefaultHome()
	}
	if cfg.ServerURL == "" || cfg.Token == "" {
		return nil, fmt.Errorf("config needs server_url and token")
	}
	home, _ := os.UserHomeDir()
	cfg.HopletHome = expandPath(cfg.HopletHome, home)
	if cfg.BrowseRoot == "" {
		cfg.BrowseRoot = home
	} else {
		cfg.BrowseRoot = expandPath(cfg.BrowseRoot, home)
	}
	if cfg.ClaudeProjects == "" {
		cfg.ClaudeProjects = filepath.Join(home, ".claude", "projects")
	} else {
		cfg.ClaudeProjects = expandPath(cfg.ClaudeProjects, home)
	}
	if cfg.ClaudeBin == "" {
		cfg.ClaudeBin = "claude"
	}
	if cfg.RuntimeIdleMinutes <= 0 {
		return nil, fmt.Errorf("runtime_idle_minutes must be positive")
	}
	if cfg.RuntimeMaxIdleProcesses < 0 {
		return nil, fmt.Errorf("runtime_max_idle_processes must not be negative")
	}
	cfg.PollInterval = time.Duration(cfg.PollIntervalMS) * time.Millisecond
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.DeviceName == "" {
		h, _ := os.Hostname()
		cfg.DeviceName = h
	}
	return cfg, nil
}

func DefaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hoplet"
	}
	return filepath.Join(home, ".hoplet")
}

func expandPath(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

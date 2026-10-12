package main

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/yangflow/hoplet/agent/internal/projects"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoArgsPrintsHelp(t *testing.T) {
	root := newRoot()
	root.SetArgs([]string{})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "daemon") || !strings.Contains(out, "projects") || !strings.Contains(out, "sessions") {
		t.Fatalf("help: %q", out)
	}
}

func TestDaemonRequiresConfig(t *testing.T) {
	root := newRoot()
	root.SetArgs([]string{"daemon", "--config", filepath.Join(t.TempDir(), "missing.json")})
	if err := root.Execute(); err == nil {
		t.Fatal("expected config load error")
	}
}

func TestCatalogUsesExplicitStateAndProviderPaths(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	project := filepath.Join(dir, "project")
	claude := filepath.Join(dir, "claude")
	fixtureDir := filepath.Join(claude, "synthetic-project")
	if err := os.MkdirAll(fixtureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := json.Marshal(map[string]any{"type": "user", "cwd": project, "message": map[string]any{"role": "user", "content": "synthetic fixture"}})
	if err := os.WriteFile(filepath.Join(fixtureDir, "fixture.jsonl"), append(fixture, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"server_url": "http://127.0.0.1:1", "token": "t", "hoplet_home": state, "claude_projects": claude, "exclude_prefixes": []string{}})
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		root := newRoot()
		root.SetArgs(append([]string{"--config", path}, args...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	run("projects", "add", project)
	run("sessions", "import", "fixture") // Resolves through the configured synthetic provider.
	file, err := projects.Load(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Items) != 1 || file.Items[0].Path != project || len(file.Items[0].Imported) != 1 || file.Items[0].Imported[0] != "fixture" {
		t.Fatalf("catalog: %+v", file.Items)
	}
	run("sessions", "remove", "fixture")
	run("projects", "remove", project)
	file, err = projects.Load(state)
	if err != nil || len(file.Items) != 0 {
		t.Fatalf("removed catalog: %+v, %v", file, err)
	}
}

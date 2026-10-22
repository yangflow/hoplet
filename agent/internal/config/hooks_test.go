package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHooks(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "notify.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	load := func(hooks string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"server_url":"http://127.0.0.1:1","token":"t","hooks":` + hooks + `}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	cfg, err := load(`[{"id":"notify","command":"` + script + `","events":["turn.completed"],"projects":["/work/a"],"env":["MY_TOKEN"]}]`)
	if err != nil || len(cfg.Hooks) != 1 || cfg.Hooks[0].ID != "notify" || cfg.Hooks[0].TimeoutMS != 10000 {
		t.Fatalf("hook not loaded: %+v %v", cfg, err)
	}
	for name, hooks := range map[string]string{
		"relative command": `[{"id":"a","command":"notify.sh","events":["*"]}]`,
		"missing file":     `[{"id":"a","command":"` + dir + `/nope","events":["*"]}]`,
		"no events":        `[{"id":"a","command":"` + script + `","events":[]}]`,
		"unknown event":    `[{"id":"a","command":"` + script + `","events":["nope"]}]`,
		"bad id":           `[{"id":"A b","command":"` + script + `","events":["*"]}]`,
		"duplicate id":     `[{"id":"a","command":"` + script + `","events":["*"]},{"id":"a","command":"` + script + `","events":["*"]}]`,
		"relative project": `[{"id":"a","command":"` + script + `","events":["*"],"projects":["work"]}]`,
		"timeout too long": `[{"id":"a","command":"` + script + `","events":["*"],"timeout_ms":60001}]`,
		"hoplet env":       `[{"id":"a","command":"` + script + `","events":["*"],"env":["HOPLET_TOKEN"]}]`,
	} {
		if _, err := load(hooks); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

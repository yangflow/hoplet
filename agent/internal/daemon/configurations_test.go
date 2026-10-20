package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/configs"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

const testSecret = "sk-never-on-the-wire-456"

// addConfiguration writes a configuration over Claude Code into the bridge's providers.json and reloads.
func addConfiguration(t *testing.T, b *bridge, id string, enabled bool) {
	t.Helper()
	home := b.cfg.HopletHome
	f, err := configs.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(configs.Configuration{ID: id, Extends: configs.Claude, Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": testSecret}}); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		off := false
		if err := f.Update(id, configs.Patch{Enabled: &off}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Save(home); err != nil {
		t.Fatal(err)
	}
	if !b.reload() {
		t.Fatal("reload did not see the change")
	}
}

func TestProvidersListEveryConfigurationWithoutValues(t *testing.T) {
	b, _, _, _, _ := newStartEnv(t)
	addConfiguration(t, b, "off", false)
	addConfiguration(t, b, "alt", true)
	got := b.providers()
	byID := map[string]sync.ProviderInfo{}
	for _, p := range got {
		byID[p.Configuration.ID] = p
	}
	if len(got) != 4 || byID["claude"].Provider != "claude" || byID["codex"].Provider != "codex" {
		t.Fatalf("providers = %+v", got)
	}
	if byID["off"].Configuration.Enabled || !byID["alt"].Configuration.Enabled {
		t.Fatalf("enabled flags: %+v", byID)
	}
	alt := byID["alt"].Configuration
	if len(alt.Env) != 1 || alt.Env[0].Name != "ANTHROPIC_AUTH_TOKEN" || !alt.Env[0].Set {
		t.Fatalf("env = %+v", alt.Env)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), testSecret) {
		t.Fatal("a value is in the provider list")
	}
}

func TestStartUnderAConfigurationRecordsItsOwner(t *testing.T) {
	b, _, _, watched, start := newStartEnv(t)
	addConfiguration(t, b, "alt", true)
	id, err := start(sync.RuntimeStart{ProjectPath: watched, Provider: "claude", Configuration: "alt", Text: "say hi", PromptID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := b.owners.Get(id); !ok || owner != "alt" {
		t.Fatalf("owner = %q %v", owner, ok)
	}
	if got := b.ownerOf(id); got != "alt" {
		t.Fatalf("ownerOf = %q", got)
	}
	// The record is on disk: a new bridge reads it.
	o, _ := configs.LoadOwners(b.cfg.HopletHome)
	if owner, _ := o.Get(id); owner != "alt" {
		t.Fatalf("owner on disk = %q", owner)
	}
}

func TestStartRefusesAnUnavailableConfiguration(t *testing.T) {
	b, _, _, watched, start := newStartEnv(t)
	addConfiguration(t, b, "off", false)
	for name, req := range map[string]sync.RuntimeStart{
		"disabled":   {Provider: "claude", Configuration: "off"},
		"unknown":    {Provider: "claude", Configuration: "nope"},
		"mismatched": {Provider: "codex", Configuration: "off"},
	} {
		req.ProjectPath, req.Text = watched, "say hi"
		if _, err := start(req); !errors.Is(err, errUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := start(sync.RuntimeStart{ProjectPath: watched, Provider: "gemini", Text: "x"}); err == nil || errors.Is(err, errUnavailable) {
		t.Errorf("unsupported provider: %v", err)
	}
}

func TestResumeRefusesWhenTheConfigurationIsGoneOrDisabled(t *testing.T) {
	b, _, _, _, _ := newStartEnv(t)
	addConfiguration(t, b, "alt", true)
	if err := b.owners.Set("s1", "alt"); err != nil {
		t.Fatal(err)
	}
	if m, err := b.resolve("s1"); err != nil || m != b.manager("alt") {
		t.Fatalf("resolve = %v %v", m, err)
	}

	f, _ := configs.Load(b.cfg.HopletHome)
	off := false
	f.Update("alt", configs.Patch{Enabled: &off})
	f.Save(b.cfg.HopletHome)
	b.reload()
	if _, err := b.resolve("s1"); !errors.Is(err, errUnavailable) {
		t.Fatalf("disabled: %v", err)
	}

	f, _ = configs.Load(b.cfg.HopletHome)
	f.Remove("alt")
	f.Save(b.cfg.HopletHome)
	b.reload()
	if _, err := b.resolve("s1"); !errors.Is(err, errUnavailable) {
		t.Fatalf("removed: %v", err)
	}
	if err := b.hooks().Prompt("s1", "hi", nil); !errors.Is(err, errUnavailable) {
		t.Fatalf("prompt: %v", err)
	}
	if errUnavailable.Error() != "configuration-unavailable" {
		t.Fatalf("code = %q", errUnavailable)
	}
}

func TestUpdateChangesOnlyNameEnabledAndDefaults(t *testing.T) {
	b, _, _, _, _ := newStartEnv(t)
	addConfiguration(t, b, "alt", true)
	name, off := "Renamed", false
	got, err := b.update(sync.DeviceProvidersUpdate{ID: "alt", Name: &name, Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if p.Configuration.ID == "alt" && (p.Configuration.Name != "Renamed" || p.Configuration.Enabled) {
			t.Fatalf("alt = %+v", p.Configuration)
		}
	}
	// The change is in the file, and the environment is untouched.
	f, _ := configs.Load(b.cfg.HopletHome)
	c, _ := f.Get("alt")
	if c.Name != "Renamed" || c.Enabled || c.Env["ANTHROPIC_AUTH_TOKEN"] != testSecret {
		t.Fatalf("alt on disk = %+v", c)
	}
}

func TestUpdateRefusals(t *testing.T) {
	b, _, _, _, _ := newStartEnv(t)
	if _, err := b.update(sync.DeviceProvidersUpdate{ID: "nope"}); !errors.Is(err, configs.ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	_, err := b.update(sync.DeviceProvidersUpdate{ID: "claude", Defaults: map[string]any{"no-such-option": "x"}})
	if !errors.Is(err, configs.ErrInvalid) {
		t.Fatalf("undeclared default: %v", err)
	}
	f, _ := configs.Load(b.cfg.HopletHome)
	if c, _ := f.Get("claude"); len(c.Defaults) != 0 {
		t.Fatalf("a refused update changed the file: %+v", c)
	}
}

func TestReloadSeesAChangeOnceAndKeepsTheLastGoodFile(t *testing.T) {
	b, _, _, _, _ := newStartEnv(t)
	if b.reload() {
		t.Fatal("nothing changed yet")
	}
	addConfiguration(t, b, "alt", true)
	if b.reload() {
		t.Fatal("the same file reloaded twice")
	}
	if err := os.WriteFile(configs.Path(b.cfg.HopletHome), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b.reload() {
		t.Fatal("a broken file must not apply")
	}
	if b.entry("alt") == nil {
		t.Fatal("the last good configurations were dropped")
	}
}

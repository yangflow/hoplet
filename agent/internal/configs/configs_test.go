package configs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const secret = "sk-test-secret-value-123"

func TestMissingFileHasTheBuiltIns(t *testing.T) {
	f, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	list := f.List()
	if len(list) != 2 || list[0].ID != Claude || list[1].ID != Codex || !list[0].Enabled || !list[1].Enabled {
		t.Fatalf("list = %+v", list)
	}
}

func TestSaveIsMode0600AndRoundTrips(t *testing.T) {
	home := t.TempDir()
	f, _ := Load(home)
	if err := f.Add(Configuration{ID: "glm", Extends: Claude, Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": secret}}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(home); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
	g, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := g.Get("glm")
	if !ok || c.Env["ANTHROPIC_AUTH_TOKEN"] != secret || c.Extends != Claude || !c.Enabled {
		t.Fatalf("glm = %+v", c)
	}
}

func TestBrokenFileKeepsTheOldOneOnDisk(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(Path(home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestBuiltInsCanOnlyBeDisabled(t *testing.T) {
	f, _ := Load(t.TempDir())
	if err := f.Remove(Claude); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("remove: %v", err)
	}
	if err := f.SetEnv(Codex, "X", "1"); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("env: %v", err)
	}
	cmd := "/bin/x"
	if err := f.Update(Claude, Patch{Command: &cmd}); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("command: %v", err)
	}
	if err := f.Add(Configuration{ID: Claude, Extends: Claude}); !errors.Is(err, ErrExists) {
		t.Fatalf("add: %v", err)
	}
	off := false
	if err := f.Update(Codex, Patch{Enabled: &off, Defaults: map[string]any{"model": "x"}}); err != nil {
		t.Fatal(err)
	}
	c, _ := f.Get(Codex)
	if c.Enabled || c.Defaults["model"] != "x" {
		t.Fatalf("codex = %+v", c)
	}
}

func TestBuiltInWithCommandOnDiskIsRefused(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(Path(home), []byte(`{"providers":[{"id":"claude","command":"/bin/x"}]}`), 0o600)
	if _, err := Load(home); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("load: %v", err)
	}
}

func TestAddRefusals(t *testing.T) {
	f, _ := Load(t.TempDir())
	for name, c := range map[string]Configuration{
		"bad id":      {ID: "Bad Id", Extends: Claude},
		"no vendor":   {ID: "x", Extends: "gemini"},
		"relative":    {ID: "x", Extends: Claude, Command: "claude"},
		"hoplet env":  {ID: "x", Extends: Claude, Env: map[string]string{"HOPLET_TOKEN": "a"}},
		"bad env key": {ID: "x", Extends: Claude, Env: map[string]string{"A B": "a"}},
	} {
		if err := f.Add(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.Add(Configuration{ID: "x", Extends: Claude}); err != nil {
		t.Fatal(err)
	}
	if err := f.Add(Configuration{ID: "x", Extends: Claude}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := f.Remove("nope"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("remove: %v", err)
	}
}

func TestEnvironStripsHopletAndOverrides(t *testing.T) {
	c := Configuration{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://x", "ANTHROPIC_AUTH_TOKEN": "", "PATH": "/custom"}}
	base := []string{"HOPLET_TOKEN=agent-secret", "PATH=/usr/bin", "ANTHROPIC_AUTH_TOKEN=from-agent", "HOME=/h"}
	got := c.Environ(base)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "HOPLET_") {
		t.Fatalf("HOPLET_ leaked: %v", got)
	}
	for _, want := range []string{"PATH=/custom", "ANTHROPIC_BASE_URL=https://x", "ANTHROPIC_AUTH_TOKEN=from-agent", "HOME=/h"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if slices.Contains(got, "PATH=/usr/bin") {
		t.Errorf("PATH not overridden: %v", got)
	}
	if !c.EnvSet("ANTHROPIC_AUTH_TOKEN", base) || c.EnvSet("MISSING", base) {
		t.Error("EnvSet")
	}
}

func TestPresetsNameTheSecretWithoutAValue(t *testing.T) {
	for _, n := range PresetNames() {
		p, _ := PresetByName(n)
		c := Configuration{Env: map[string]string{}}
		p.Apply(&c)
		v, ok := c.Env[p.Secret]
		if !ok || v != "" {
			t.Errorf("%s: secret %q = %q, %v", n, p.Secret, v, ok)
		}
	}
	for _, n := range []string{"glm", "zai", "deepseek", "minimax", "minimax-cn", "qwen"} {
		if _, ok := PresetByName(n); !ok {
			t.Errorf("preset %s missing", n)
		}
	}
}

func TestOwnersPersistWith0600(t *testing.T) {
	home := t.TempDir()
	o, err := LoadOwners(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Set("s1", "glm"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, "sessions-config.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat: %v %v", fi, err)
	}
	o2, _ := LoadOwners(home)
	if id, ok := o2.Get("s1"); !ok || id != "glm" {
		t.Fatalf("get = %q %v", id, ok)
	}
	o2.Forget("s1")
	if _, ok := o2.Get("s1"); ok {
		t.Fatal("not forgotten")
	}
	// The removal is on disk, not only in memory.
	o3, _ := LoadOwners(home)
	if _, ok := o3.Get("s1"); ok {
		t.Fatal("entry still in sessions-config.json")
	}
}

func run(t *testing.T, home, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := NewCommand(func() string { return home }, strings.NewReader(stdin), &out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCLINeverPrintsValues(t *testing.T) {
	home := t.TempDir()
	var all strings.Builder
	steps := []struct {
		stdin string
		args  []string
	}{
		{"", []string{"add", "glm", "--extends", "claude", "--preset", "glm"}},
		{secret + "\n", []string{"env", "glm", "ANTHROPIC_AUTH_TOKEN"}},
		{"", []string{"set", "glm", "--default", "model=glm-x", "--name", "GLM"}},
		{"", []string{"list"}},
	}
	for _, s := range steps {
		out, err := run(t, home, s.stdin, s.args...)
		if err != nil {
			t.Fatalf("%v: %v", s.args, err)
		}
		all.WriteString(out)
	}
	if strings.Contains(all.String(), secret) {
		t.Fatalf("a value was printed:\n%s", all.String())
	}
	if !strings.Contains(all.String(), "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("the name is not listed:\n%s", all.String())
	}
	f, _ := Load(home)
	c, _ := f.Get("glm")
	if c.Env["ANTHROPIC_AUTH_TOKEN"] != secret || c.Name != "GLM" || c.Defaults["model"] != "glm-x" {
		t.Fatalf("glm = %+v", c)
	}
	if _, err := run(t, home, "", "remove", "glm"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, home, "", "remove", "claude"); err == nil {
		t.Fatal("removing a built-in must fail")
	}
	if _, err := run(t, home, "\n", "env", "codex", "X"); err == nil {
		t.Fatal("empty value must fail")
	}
}

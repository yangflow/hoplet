package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func TestEnsureDeviceIDTrimsFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "device-id"), []byte("abcdef0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := ensureDeviceID(&config.Config{HopletHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if id != "abcdef0123456789" {
		t.Fatalf("got %q", id)
	}
}

func TestEnsureDeviceIDUsesConfig(t *testing.T) {
	id, err := ensureDeviceID(&config.Config{HopletHome: t.TempDir(), DeviceID: "from-config-id"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "from-config-id" {
		t.Fatalf("got %q", id)
	}
}

func TestConfiguredStateKeepsGeneratedIdentity(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	path := filepath.Join(dir, "config.json")
	raw := `{"server_url":"http://127.0.0.1:1","token":"t","hoplet_home":"` + state + `"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ensureDeviceID(cfg)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(state, "device-id"))
	if err != nil || string(saved) != id {
		t.Fatalf("saved identity differs: %v", err)
	}
	again, err := ensureDeviceID(cfg)
	if err != nil || again != id {
		t.Fatalf("identity changed: %v", err)
	}
}

package sync

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func TestDeviceInventoryWithoutAConnectionIsDropped(t *testing.T) {
	c := NewClient(&config.Config{ServerURL: "http://127.0.0.1", Token: "t"}, "dev", "test", Hooks{})
	if err := c.SendDeviceInventory(DeviceInventory{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeviceInventoryWireShape(t *testing.T) {
	mem := uint64(24 << 30)
	origin := "https://github.com/o/r.git"
	raw, err := json.Marshal(DeviceInventory{
		V: ProtocolVersion, Type: TypeDeviceInventory, At: 7,
		Toolchains:   []InventoryToolchain{{Name: "git", Version: "2.45.1"}},
		Gpus:         []InventoryGpu{{Name: "RTX", Memory: &mem}, {Name: "Apple M3"}},
		Repositories: []InventoryRepository{{Path: "/a", Origin: &origin}, {Path: "/b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":14,"type":"device.inventory","at":7,"toolchains":[{"name":"git","version":"2.45.1"}],` +
		`"gpus":[{"name":"RTX","memory":25769803776},{"name":"Apple M3"}],` +
		`"repositories":[{"path":"/a","origin":"https://github.com/o/r.git"},{"path":"/b","origin":null}]}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
}

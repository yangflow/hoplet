package sync

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func TestDeviceResourcesWithoutAConnectionIsDropped(t *testing.T) {
	c := NewClient(&config.Config{ServerURL: "http://127.0.0.1", Token: "t"}, "dev", "test", Hooks{})
	if err := c.SendDeviceResources(DeviceResources{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeviceResourcesWireShape(t *testing.T) {
	cpu := 12.5
	raw, err := json.Marshal(DeviceResources{
		V: ProtocolVersion, Type: TypeDeviceResources, At: 1, MemUsed: 2, MemTotal: 3, DiskFree: 4, DiskTotal: 5,
		Sessions: []SessionResources{{SessionID: "s", CPU: &cpu, RSS: 6, Children: 1}, {SessionID: "t"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":24,"type":"device.resources","at":1,"cpu":null,"memUsed":2,"memTotal":3,"diskFree":4,"diskTotal":5,` +
		`"sessions":[{"sessionId":"s","cpu":12.5,"rss":6,"children":1},{"sessionId":"t","cpu":null,"rss":0,"children":0}]}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
}

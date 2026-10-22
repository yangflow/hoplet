package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

func TestCapabilitiesSayWhenRulesActOnPromptsOnly(t *testing.T) {
	got := capabilities([]runtime.Capability{runtime.CapPermissions, runtime.CapRulesPromptsOnly}, nil)
	if got.Rules != sync.RulesPromptsOnly || !got.Permissions {
		t.Fatalf("%+v", got)
	}
	b, _ := json.Marshal(capabilities([]runtime.Capability{runtime.CapPermissions}, nil))
	if strings.Contains(string(b), `"rules"`) {
		t.Errorf("a driver with the hook declared %s", b)
	}
}

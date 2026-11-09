package daemon

import (
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestMemoryMethodsStayLocal(t *testing.T) {
	for _, m := range []string{proto.MethodProfileSettings, proto.MethodProfileSettingsSet, proto.MethodProfileUnlink, proto.MethodProfileUsage} {
		if !localOnly(m) {
			t.Errorf("%s is open to paired devices", m)
		}
	}
}

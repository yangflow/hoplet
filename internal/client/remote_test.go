package client

import (
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/noise"
)

// Dialing over Noise is tested against a real daemon in package daemon;
// these are the checks made before any route is tried.
func TestDialRemoteChecks(t *testing.T) {
	id, err := noise.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		r     Remote
		inErr string
	}{
		"bad key":   {Remote{Routes: []string{"ws://127.0.0.1:1"}, HostKey: []byte{1}, Identity: id}, "host key"},
		"no routes": {Remote{HostKey: id.Public(), Identity: id}, "no routes"},
	} {
		if _, err := DialRemote(t.Context(), tc.r); err == nil || !strings.Contains(err.Error(), tc.inErr) {
			t.Errorf("%s: %v, want %q", name, err, tc.inErr)
		}
	}
}

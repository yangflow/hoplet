package main

import (
	"bytes"
	"testing"
)

func TestBridgeUsage(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want int
	}{
		"help":     {[]string{"--help"}, 0},
		"no flag":  {nil, 2},
		"extra":    {[]string{"--hermes", "X", "extra"}, 2},
		"bad flag": {[]string{"--nope"}, 2},
	} {
		var out, errb bytes.Buffer
		if code := runBridge(c.args, &bytes.Buffer{}, &out, &errb); code != c.want {
			t.Errorf("hermes-bridge %s: exit %d, want %d", name, code, c.want)
		}
		if out.Len() != 0 {
			t.Errorf("hermes-bridge %s wrote to stdout: %q", name, out.String())
		}
	}
}

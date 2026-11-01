package main

import (
	"bytes"
	"testing"
)

func TestBridgeUsage(t *testing.T) {
	for args, want := range map[string]int{"--help": 0, "": 2, "extra": 2} {
		var out, errb bytes.Buffer
		argv := []string{}
		if args != "" {
			argv = append(argv, args)
		}
		if code := runBridge(argv, &bytes.Buffer{}, &out, &errb); code != want {
			t.Errorf("hermes-bridge %q: exit %d, want %d", args, code, want)
		}
		if out.Len() != 0 {
			t.Errorf("hermes-bridge %q wrote to stdout: %q", args, out.String())
		}
	}
}

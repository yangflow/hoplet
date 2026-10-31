package main

import (
	"bytes"
	"testing"
)

func TestShimUsage(t *testing.T) {
	for _, args := range [][]string{
		{"shim"},
		{"shim", "--dir", "/tmp/x"},
		{"shim", "--", "true"},
		{"shim", "--bogus"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%q: unexpected stdout %q", args, out.String())
		}
		if !bytes.Contains(errb.Bytes(), []byte("usage")) && !bytes.Contains(errb.Bytes(), []byte("flag provided but not defined")) {
			t.Errorf("%q: stderr %q", args, errb.String())
		}
	}
}

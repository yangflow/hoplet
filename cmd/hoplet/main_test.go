package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		code     int
		stdout   string
		inStderr string
	}{
		{"version", []string{"version"}, 0, "dev\n", ""},
		{"no args", nil, 2, "", "usage: hoplet"},
		{"unknown", []string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(c.args, &out, &errb); got != c.code {
				t.Fatalf("exit %d, want %d", got, c.code)
			}
			if out.String() != c.stdout {
				t.Fatalf("stdout %q, want %q", out.String(), c.stdout)
			}
			if !bytes.Contains(errb.Bytes(), []byte(c.inStderr)) {
				t.Fatalf("stderr %q, want it to contain %q", errb.String(), c.inStderr)
			}
		})
	}
}

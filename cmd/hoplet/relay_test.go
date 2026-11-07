package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelayAllowlist(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOPLET_HOME", h)
	const a, b = "abcdefghijklmnopqrstuvwxyz", "234567abcdefghijklmnopqrst"
	mustCLI(t, "relay", "allow", a)
	mustCLI(t, "relay", "allow", b)
	if out := mustCLI(t, "relay", "list"); out != a+"\n"+b+"\n" {
		t.Fatalf("list: %q", out)
	}
	fi, err := os.Stat(filepath.Join(h, "relay", "allow"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("allowlist mode: %v %v", fi.Mode().Perm(), err)
	}
	mustCLI(t, "relay", "deny", a)
	if out := mustCLI(t, "relay", "list", "--json"); !strings.Contains(out, b) || strings.Contains(out, a) {
		t.Fatalf("list after deny: %q", out)
	}
	if code, _, errs := cli(t, "relay", "allow", "not-an-id"); code != 1 || !strings.Contains(errs, "not a host ID") {
		t.Fatalf("bad ID: %d %q", code, errs)
	}
	other := t.TempDir()
	mustCLI(t, "relay", "allow", "--dir", other, a)
	if out := mustCLI(t, "relay", "list", "--dir", other); out != a+"\n" {
		t.Fatalf("list --dir: %q", out)
	}
}

func TestRelayUsageErrors(t *testing.T) {
	t.Setenv("HOPLET_HOME", t.TempDir())
	for _, args := range [][]string{
		{"relay"},
		{"relay", "--listen", "127.0.0.1:0"},
		{"relay", "--domain", "https://relay.example.com"},
		{"relay", "allow"},
		{"relay", "list", "extra"},
		{"--host", "b", "relay", "list"},
	} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("hoplet %s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
}
